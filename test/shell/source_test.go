package shell_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/shellquote"
)

// TestPinnedVersionWins is property 1 in its most direct form: after activation,
// the name resolves to the version this repository pins, not to the same-named
// binary sitting later on PATH.
func TestPinnedVersionWins(t *testing.T) {
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			assertRan(t, f.run(shell, toolName+"\n"), branchV1)
		})
	}
}

// TestBranchSwitchOnASingleLine is property 2, and it is the reason source mode
// is not a prompt hook. `git checkout v2 && stub-tool` renders no prompt between
// the two commands, so a hook-based activation never fires and the previous
// branch's binary runs — exiting 0, printing plausible output, from a version
// the tree no longer pins. Both directions are checked: a switch that goes
// forward and stays there proves nothing about one that comes back.
func TestBranchSwitchOnASingleLine(t *testing.T) {
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			assertRan(t, f.run(shell, chain(shell, "git checkout -q "+branchV2, toolName)), branchV2)
			assertRan(t, f.run(shell, chain(shell, "git checkout -q "+branchV1, toolName)), branchV1)
		})
	}
}

// chain joins two commands so the second runs only if the first succeeded, with
// no newline between them — fish spells the operator differently, and the point
// of the test is that there is no opportunity for a prompt to render.
func chain(shell string, first, second string) string {
	if shell == "fish" {
		return first + "; and " + second + "\n"
	}
	return first + " && " + second + "\n"
}

// TestBranchSwitchWithoutAPrompt is property 2 in the contexts where a prompt
// hook could not run even in principle: a make recipe, a non-interactive shell
// started by a script, and a POSIX sh that knows nothing about datamitsu. These
// are where CI and git hooks live.
func TestBranchSwitchWithoutAPrompt(t *testing.T) {
	f := newFixture(t)

	cases := []struct {
		name string
		run  string
	}{
		{"make", "make version"},
		{"bash -c", "bash --noprofile --norc -c " + shellquote.Bash(toolName)},
		{"sh -c", "sh -c " + shellquote.Bash(toolName)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "make" {
				if _, err := exec.LookPath("make"); err != nil {
					t.Skip("make is not installed; branch switching through a make recipe is unverified on this machine")
				}
			}
			// Rewind so the activation below bakes v1 and the switch inside the
			// script is the thing under test.
			f.git("checkout", "-q", branchV1)
			assertRan(t, f.run("bash", chain("bash", "git checkout -q "+branchV2, tc.run)), branchV2)
		})
	}
}

// TestLazyMaterialization is property 3: activation downloads nothing, and the
// tool arrives on its first real use. The store is inspected directly rather
// than through datamitsu, so the assertion does not depend on the same code
// deciding what "installed" means.
func TestLazyMaterialization(t *testing.T) {
	f := newFixture(t)

	// Activating bakes the farm. Nothing is fetched.
	f.activation("bash")
	if entries := f.storeEntries(); len(entries) != 0 {
		t.Fatalf("activation downloaded %v; it must download nothing", entries)
	}

	assertRan(t, f.run("bash", toolName+"\n"), branchV1)
	if entries := f.storeEntries(); len(entries) != 1 {
		t.Fatalf("store holds %v after first use, want exactly one entry", entries)
	}

	// The exit code is the tool's, not the shim's: a 127 here would mean the
	// install path swallowed the invocation instead of handing it over.
	if res := f.run("bash", toolName+" --exit 42\n"); res.ExitCode != 42 {
		t.Fatalf("exit code after lazy materialization = %d, want 42\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
}

// TestDeletedStoreEntryIsRepaired covers the other half of lazy materialization:
// a store that lost the file the farm points at. `source refresh --force` is the
// documented repair, and after it the tool runs again — the farm is never left
// pointing at nothing.
func TestDeletedStoreEntryIsRepaired(t *testing.T) {
	f := newFixture(t)
	assertRan(t, f.run("bash", toolName+"\n"), branchV1)

	for _, entry := range f.storeEntries() {
		if err := os.RemoveAll(entry); err != nil {
			t.Fatalf("delete the store entry: %v", err)
		}
	}
	if entries := f.storeEntries(); len(entries) != 0 {
		t.Fatalf("store still holds %v", entries)
	}

	if res := f.datamitsu("source", "refresh", "--force"); res.ExitCode != 0 {
		t.Fatalf("`source refresh --force` exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	assertRan(t, f.run("bash", toolName+"\n"), branchV1)
}

// TestUndeclaredNameExits127 is D4, the decision that keeps the feature's worst
// failure mode off the table. A branch that stops declaring a tool must not
// hand the name back to PATH, where a stale system binary is waiting: it exits
// 127, the way a shell reports a command it cannot find.
func TestUndeclaredNameExits127(t *testing.T) {
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			// Prove the impostor is reachable, so the assertion below is about
			// datamitsu refusing it and not about it being absent.
			if res := f.runRaw(shell, toolName+"\n"); !strings.Contains(res.Stdout, impostorOutput) {
				t.Fatalf("the planted impostor is not on PATH; the test would pass vacuously:\n%s", res.Stdout)
			}

			res := f.run(shell, chain(shell, "git checkout -q "+branchNone, toolName))
			if res.ExitCode != 127 {
				t.Fatalf("exit = %d, want 127\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
			}
			if strings.Contains(res.Stdout, impostorOutput) {
				t.Fatalf("PATH fell through to the system binary:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stderr, toolName) {
				t.Errorf("stderr does not name the app that failed:\n%s", res.Stderr)
			}
		})
	}
}

// TestExitCodesPropagate asserts the shim is transparent to the shell's own
// control flow. syscall.Exec is what makes this true by construction; a child
// process would have to forward it, and 42 is the value that catches a
// truncated or remapped code.
func TestExitCodesPropagate(t *testing.T) {
	f := newFixture(t)
	// Materialize first, so the codes under test are the tool's steady state
	// rather than the install path's.
	assertRan(t, f.run("bash", toolName+"\n"), branchV1)

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			// The fixture is built once above, so it must be rebound here or a
			// missing shell skips the parent instead of this subtest.
			f := f.with(t)
			for _, want := range []int{0, 1, 42} {
				res := f.run(shell, toolName+" --exit "+strconv.Itoa(want)+"\n")
				if res.ExitCode != want {
					t.Errorf("exit = %d, want %d\nstderr:\n%s", res.ExitCode, want, res.Stderr)
				}
			}
		})
	}
}

// TestArgvPassesVerbatim is a correctness requirement, not a nicety:
// `datamitsu exec actionlint --version` fails today because cobra parses the
// tool's flags. The shim bypasses cobra entirely, so a leading flag, an argument
// with a space, one with a quote and one with a newline all arrive untouched.
func TestArgvPassesVerbatim(t *testing.T) {
	args := []string{"--version", "a b", "it's", "x\ny", "*"}
	var want strings.Builder
	for _, a := range args {
		want.WriteString("<" + a + ">\n")
	}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			quote := shellquote.Bash
			if shell == "fish" {
				quote = shellquote.Fish
			}
			cmd := toolName + " --argv"
			var cmdSb198 strings.Builder
			for _, a := range args {
				cmdSb198.WriteString(" " + quote(a))
			}
			cmd += cmdSb198.String()

			f := newFixture(t)
			res := f.run(shell, cmd+"\n")
			if res.ExitCode != 0 {
				t.Fatalf("exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
			}
			if res.Stdout != want.String() {
				t.Errorf("argv arrived as:\n%q\nwant:\n%q", res.Stdout, want.String())
			}
		})
	}
}

// TestHostileFarmPath runs the whole feature with a farm directory holding a
// space, a single quote and a glob character. Shell code that is merely
// plausible breaks here — an unquoted path splits into words, an unescaped
// bracket is a glob that matches nothing and silently expands to itself — and no
// amount of Go-level testing can see it.
func TestHostileFarmPath(t *testing.T) {
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			cache := filepath.Join(t.TempDir(), "ca'che [dir] $x")
			f := newFixtureWithCache(t, cache)

			farm := f.farmDir()
			if !strings.Contains(farm, "'") || !strings.Contains(farm, "[") || !strings.Contains(farm, " ") {
				t.Fatalf("the farm path is not hostile enough to prove anything: %q", farm)
			}
			assertRan(t, f.run(shell, toolName+"\n"), branchV1)
		})
	}
}

// TestActivationIsIdempotent asserts a shell rc file can call the activation
// unconditionally: PATH gains exactly one entry no matter how many times it
// runs. fish is the interesting half — fish_add_path without --move silently
// does nothing when the directory is already present, which looks identical
// until the farm needs to move back to the front.
func TestActivationIsIdempotent(t *testing.T) {
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			farm := f.farmDir()
			activation := f.activation(shell)

			dumpPath := "printf '%s\\n' \"$PATH\"\n"
			if shell == "fish" {
				dumpPath = "for p in $PATH; printf '%s\\n' $p; end\n"
			}
			res := f.runRaw(shell, activation+activation+dumpPath)
			if res.ExitCode != 0 {
				t.Fatalf("re-activation failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
			}
			if n := strings.Count(res.Stdout, farm); n != 1 {
				t.Errorf("the farm appears %d times on PATH after two activations, want 1:\n%s", n, res.Stdout)
			}
		})
	}
}
