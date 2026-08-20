package shell_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/shellquote"
)

// This file is the machine-level half of the tier: `datamitsu source <shell>
// --config <path>` from a shell rc file, which is the invocation that puts a
// person's own toolchain on PATH in every shell rather than one repository's.
//
// Everything here needs a real shell for the same reason the project cases do —
// PATH layering, the order two activations end up in, and whether a name found
// through one farm's bin directory is answered by that farm are properties of
// process execution, not of any Go-level abstraction over it.
//
// configShells is the set the plan requires. zsh shares bash's renderer and is
// covered by the project cases in source_test.go; repeating every machine-level
// case in it would triple the tier's runtime for one already-proved property.
var configShells = []string{"bash", "fish"}

// TestMachineFarmActivatesOutsideAnyRepository is the use case in its most
// direct form: no git root anywhere above the shell, and the tool the config
// names resolves to the version that config pins — not to the same-named binary
// planted later on PATH.
func TestMachineFarmActivatesOutsideAnyRepository(t *testing.T) {
	for _, shell := range configShells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			script := f.machineActivation(shell) + toolName + "\n"
			assertRanTool(t, f.runRawIn(shell, f.MachineDir, script), toolName, machineVersion)
		})
	}
}

// TestMachineFarmNeverEvaluatesProjectConfig is the trust boundary, proved
// rather than assumed. A shell activated against a machine-level config that
// walks into a repository it never activated keeps running the machine-level tools,
// and never resolves that repository's config — so the config here throws the
// moment anything evaluates it. An assertion on the version alone would pass
// even if the config had been read and merged; the throw is what makes reading
// it observable.
func TestMachineFarmNeverEvaluatesProjectConfig(t *testing.T) {
	for _, shell := range configShells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			activation := f.machineActivation(shell)
			f.poisonProjectConfig()

			// Prove the poison bites, so the assertion below is about the shim
			// declining to resolve this repository and not about a config that
			// happens to be harmless. This activation fails, so it bakes nothing.
			if res := f.datamitsu("source", "bash"); res.ExitCode == 0 || !strings.Contains(res.Stderr, poisonMarker) {
				t.Fatalf("evaluating the repository's config does not fail; the test would pass vacuously:\n"+
					"exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
			}

			// cwd is the repository root, and the repository declares the same
			// tool name at a version of its own.
			res := f.runRawIn(shell, f.Dir, activation+toolName+"\n")
			assertRanTool(t, res, toolName, machineVersion)
			if strings.Contains(res.Stdout+res.Stderr, poisonMarker) {
				t.Fatalf("the repository's config was evaluated:\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			}
			if farms := f.projectFarms(); len(farms) != 0 {
				t.Fatalf("a machine-level activation baked a project farm: %v", farms)
			}
		})
	}
}

// TestProjectFarmWinsOverMachineFarm is the layering rule, and the point is that
// there is no rule: the project activation runs second, so its farm lands ahead
// on PATH and its pin answers. Dropping that one directory from PATH — with
// nothing else changed and no datamitsu command run — returns the machine-level
// version, which is only true if ordinary PATH order is the whole mechanism.
func TestProjectFarmWinsOverMachineFarm(t *testing.T) {
	for _, shell := range configShells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			both := f.machineActivation(shell) + f.activation(shell)

			assertRan(t, f.runRawIn(shell, f.Dir, both+toolName+"\n"), branchV1)

			// The mechanism is visible: both farms are on PATH, the project's
			// ahead of the machine's. There is nothing else to inspect, which is
			// the property — a precedence table would not show up here at all.
			order := f.runRawIn(shell, f.Dir, both+dumpPath(shell))
			project, machine := strings.Index(order.Stdout, f.farmDir()), strings.Index(order.Stdout, f.machineFarmDir())
			if project < 0 || machine < 0 || project > machine {
				t.Fatalf("PATH does not hold the project farm ahead of the machine farm:\n%s", order.Stdout)
			}

			drop := dropFromPath(shell, f.farmDir())
			res := f.runRawIn(shell, f.Dir, both+drop+toolName+"\n")
			assertRanTool(t, res, toolName, machineVersion)
		})
	}
}

// dumpPath renders shell code printing PATH one entry per line.
func dumpPath(shell string) string {
	if shell == "fish" {
		return "for p in $PATH; printf '%s\\n' $p; end\n"
	}
	return "printf '%s\\n' \"$PATH\"\n"
}

// dropFromPath renders shell code removing one directory from PATH. bash's
// substitution is the same idiom the activation itself uses, so a path holding
// shell metacharacters is handled the same way in both; `hash -r` matters
// because bash would otherwise answer the next invocation from its cache of
// where the name used to live.
func dropFromPath(shell, dir string) string {
	if shell == "fish" {
		return "set -gx PATH (string match -v -- " + shellquote.Fish(dir) + " $PATH)\n"
	}
	return "__drop=" + shellquote.Bash(dir) + "\n" +
		"__p=\":$PATH:\"\n" +
		"__p=\"${__p//:\"$__drop\":/:}\"\n" +
		"__p=\"${__p#:}\"\n" +
		"__p=\"${__p%:}\"\n" +
		"PATH=\"$__p\"\n" +
		"export PATH\n" +
		"unset __drop __p\n" +
		"hash -r\n"
}

// TestBranchSwitchUnderMachineFarm asserts the machine-level farm underneath
// changes nothing about the property source mode exists for. A second farm on
// PATH is exactly the situation in which a branch switch could silently stop
// taking effect — the name still resolves, to the machine-level version, and
// exits 0 while printing plausible output.
func TestBranchSwitchUnderMachineFarm(t *testing.T) {
	for _, shell := range configShells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			both := f.machineActivation(shell) + f.activation(shell)

			// Both directions: a switch that goes forward and stays there proves
			// nothing about one that comes back to a version already baked.
			res := f.runRawIn(shell, f.Dir, both+chain(shell, "git checkout -q "+branchV2, toolName))
			assertRan(t, res, branchV2)

			res = f.runRawIn(shell, f.Dir, both+chain(shell, "git checkout -q "+branchV1, toolName))
			assertRan(t, res, branchV1)
		})
	}
}

// TestMachineOnlyToolReachableInsideProject is the other half of layering: a
// project farm sitting first must shadow only the names it declares. A tool that
// exists nowhere but the machine-level config stays reachable from inside the
// project, which is what makes a machine-level farm worth having at all — a farm
// that vanished on `cd` into a repository would be useless in every repository.
func TestMachineOnlyToolReachableInsideProject(t *testing.T) {
	for _, shell := range configShells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			both := f.machineActivation(shell) + f.activation(shell)

			res := f.runRawIn(shell, f.Dir, both+machineOnlyTool+"\n")
			assertRanTool(t, res, machineOnlyTool, machineVersion)
		})
	}
}

// TestMachineActivationDownloadsNothing is lazy materialization for the
// machine-level tier, and it matters more here than for a project: this
// activation runs in every shell a person opens, so anything it fetches is paid
// for on every terminal, every tmux pane and every `bash -c` in a script.
func TestMachineActivationDownloadsNothing(t *testing.T) {
	f := newFixture(t)

	f.machineActivation("bash")
	if entries := f.storeEntries(); len(entries) != 0 {
		t.Fatalf("the machine-level activation downloaded %v; it must download nothing", entries)
	}

	script := f.machineActivation("bash") + toolName + "\n"
	assertRanTool(t, f.runRawIn("bash", f.MachineDir, script), toolName, machineVersion)
	if entries := f.storeEntries(); len(entries) != 1 {
		t.Fatalf("store holds %v after first use, want exactly one entry", entries)
	}
}
