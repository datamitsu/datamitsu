package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// These tests cover the machine-level toolchain: `datamitsu source <shell>
// --config <path>` from a directory that is not inside any repository, which is
// the invocation a shell rc file carries.
//
// The farm such an activation writes is an explicit-config farm — identity
// hashed from the resolved config chain, no git root, and a shim that
// revalidates only that chain. What it must never do is discover: a machine-level
// farm baked from inside a repository would otherwise silently merge in that
// repository's config, and the first rebake (which always carries
// --no-auto-config) would just as silently drop everything it contributed.

// machineConfigJS is a config outside every repository. It declares one binary
// app that will never be downloaded (offline, so it stays a shim entry), one
// shell app and one deny-listed name, so a single fixture exercises the entry
// list and both exclusion reasons.
//
// It discards its input rather than spreading it, so the app list is exactly
// what this file declares and a merged-in discovered config is visible as an
// extra name.
const machineConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: {
    "machine-tool": { binary: { binaries: {
      linux: { amd64: { glibc: { url: "https://example.test/x.tar.gz", hash: "` + machineHashA + `", contentType: "tar.gz" } } },
      darwin: { arm64: { unknown: { url: "https://example.test/y.tar.gz", hash: "` + machineHashB + `", contentType: "tar.gz" } } }
    } } },
    echo: { shell: { name: "echo" } },
    sudo: { binary: { binaries: {
      linux: { amd64: { glibc: { url: "https://example.test/z.tar.gz", hash: "` + machineHashA + `", contentType: "tar.gz" } } },
      darwin: { arm64: { unknown: { url: "https://example.test/w.tar.gz", hash: "` + machineHashB + `", contentType: "tar.gz" } } }
    } } }
  },
  tools: {}, projectTypes: {}
});
globalThis.getMinVersion = () => "0.0.0";
`

// Hashes are mandatory for every downloadable artifact, so the fixture carries
// well-formed SHA-256 values it will never verify: nothing is fetched here.
const (
	machineHashA = "1111111111111111111111111111111111111111111111111111111111111111"
	machineHashB = "2222222222222222222222222222222222222222222222222222222222222222"
)

// writeMachineConfig puts the machine-level config in a directory git knows
// nothing about, which is the whole point of the fixture.
func writeMachineConfig(t *testing.T) (*clitest.BareDir, string) {
	t.Helper()
	d := clitest.NewBareDir(t)
	return d, d.WriteFile("machine.config.js", machineConfigJS)
}

// emptyMachineConfigJS is the machine-level counterpart of sourceAutoConfigJS:
// a config outside every repository declaring no apps at all, so the activation
// it produces depends on nothing but the farm path and the chain that names it.
// Goldens use it rather than machineConfigJS, whose exclusions write reasons to
// stderr that belong to TestSourceConfigExclusionsStillApply.
const emptyMachineConfigJS = "globalThis.getBeforeConfigs = () => [];\n" +
	"globalThis.getConfig = () => ({ apps: {}, tools: {}, projectTypes: {} });\n" +
	"globalThis.getMinVersion = () => \"0.0.0\";\n"

// configFarmHashRE matches the per-chain directory name in a config farm path.
// The farm lives under {cache}/configs/{XXH3-128(resolved chain)}/, so the
// segment fingerprints a temp directory that differs on every run — masking the
// enclosing cache path is not enough to make the output comparable. It is the
// exact counterpart of farmHashRE for the other namespace.
var configFarmHashRE = regexp.MustCompile(`configs[/\\][0-9a-f]{32}`)

// sourceConfigNormalizer returns the normalizer every machine-level source
// golden shares: the bare directory holding the config, the isolated cache dir,
// and the per-chain farm fingerprint that derives from the former.
func sourceConfigNormalizer(dir, cacheBase string) func(string) string {
	norm := clitest.NewNormalizer().MaskPath(dir, "<TMP>").MaskPath(cacheBase, "<CACHE>")
	return func(s string) string {
		return configFarmHashRE.ReplaceAllString(norm.Apply(s), "configs/<CHAIN>")
	}
}

// TestSourceConfigActivationGolden freezes the exact activation block a shell rc
// file gets for a machine-level toolchain, in all three shells. Substring
// assertions elsewhere in this file state individual properties; the golden is
// what fails on a byte that no property thought to forbid — a progress line, a
// log line, a stray warning — in output that is piped straight into `eval`.
func TestSourceConfigActivationGolden(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			d := clitest.NewBareDir(t)
			cfg := d.WriteFile("machine.config.js", emptyMachineConfigJS)
			cacheBase := t.TempDir()
			mask := sourceConfigNormalizer(d.Dir, cacheBase)

			res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir, CacheDir: cacheBase, Env: sourceEnv()},
				"source", shell, "--config", cfg)
			if res.ExitCode != 0 {
				t.Fatalf("`source %s --config <path>` exit = %d, want 0\nstderr:\n%s", shell, res.ExitCode, res.Stderr)
			}
			if res.Stderr != "" {
				t.Errorf("`source %s --config <path>` wrote to stderr on the clean path:\n%s", shell, res.Stderr)
			}
			clitest.AssertGolden(t, "source_config_"+shell, mask(res.Stdout))
		})
	}
}

// TestSourceConfigStatusGolden freezes the machine-level status report, human
// and JSON. It is the surface the plan calls out as mattering most here: a farm
// that is first on PATH in every shell is one the user has to be able to read
// back — which origin it has, and which files it was baked from.
func TestSourceConfigStatusGolden(t *testing.T) {
	for _, tc := range []struct {
		golden string
		args   []string
	}{
		{"source_config_status", []string{"source", "status"}},
		{"source_config_status_json", []string{"source", "status", "--json"}},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			d := clitest.NewBareDir(t)
			cfg := d.WriteFile("machine.config.js", emptyMachineConfigJS)
			cacheBase := t.TempDir()
			mask := sourceConfigNormalizer(d.Dir, cacheBase)

			args := append(append([]string(nil), tc.args...), "--config", cfg)
			res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir, CacheDir: cacheBase, Env: sourceEnv()}, args...)
			if res.ExitCode != 0 {
				t.Fatalf("`%s` exit = %d, want 0\nstderr:\n%s", strings.Join(args, " "), res.ExitCode, res.Stderr)
			}
			clitest.AssertGolden(t, tc.golden, mask(res.Stdout))
		})
	}
}

// TestSourceExplicitConfigCannotInjectIntoStdout is the config-injection guard
// applied to a config the user names explicitly. Naming the file is the trust boundary
// for *evaluating* it, not permission for it to write to stdout: the output is
// piped into `eval` in a shell rc file, so a console.log would be executing its
// own text in every shell the user opens. The activation is compared against a
// control chain whose config is identical minus the printing, so a single extra
// byte fails.
func TestSourceExplicitConfigCannotInjectIntoStdout(t *testing.T) {
	noisy := clitest.NewBareDir(t)
	noisyCfg := noisy.WriteFile("machine.config.js",
		"console.log(\"echo pwned\");\n"+
			"globalThis.getBeforeConfigs = () => [];\n"+
			"globalThis.getConfig = () => { console.log(\"rm -rf /\"); "+
			"return { apps: {}, tools: {}, projectTypes: {} }; };\n"+
			"globalThis.getMinVersion = () => \"0.0.0\";\n")
	noisyCache := t.TempDir()
	noisyRes := clitest.Run(t, clitest.RunOptions{Dir: noisy.Dir, CacheDir: noisyCache, Env: sourceEnv()},
		"source", "bash", "--config", noisyCfg)
	if noisyRes.ExitCode != 0 {
		t.Fatalf("`source bash --config <path>` exit = %d, want 0\nstderr:\n%s", noisyRes.ExitCode, noisyRes.Stderr)
	}

	control := clitest.NewBareDir(t)
	controlCfg := control.WriteFile("machine.config.js", emptyMachineConfigJS)
	controlCache := t.TempDir()
	controlRes := clitest.Run(t, clitest.RunOptions{Dir: control.Dir, CacheDir: controlCache, Env: sourceEnv()},
		"source", "bash", "--config", controlCfg)
	if controlRes.ExitCode != 0 {
		t.Fatalf("control `source bash --config <path>` exit = %d, want 0\nstderr:\n%s",
			controlRes.ExitCode, controlRes.Stderr)
	}

	got := sourceConfigNormalizer(noisy.Dir, noisyCache)(noisyRes.Stdout)
	want := sourceConfigNormalizer(control.Dir, controlCache)(controlRes.Stdout)
	if got != want {
		t.Errorf("config JS changed the activation on stdout:\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
	for _, injected := range []string{"pwned", "rm -rf"} {
		if strings.Contains(noisyRes.Stdout, injected) {
			t.Errorf("config console.log reached stdout (%q):\n%s", injected, noisyRes.Stdout)
		}
	}
}

// TestSourceConfigUnusableChainFails asserts a --config that cannot be resolved
// is a loud failure with an empty stdout, for both ways it can be unusable: the
// path is not there, and the file is there but is not valid JavaScript.
//
// Empty stdout is the load-bearing half. The activation is consumed as
// `eval "$(datamitsu source bash --config …)"`, so a diagnostic that leaked onto
// stdout would be run as shell code by the very shell that failed to activate.
func TestSourceConfigUnusableChainFails(t *testing.T) {
	d := clitest.NewBareDir(t)
	missing := filepath.Join(d.Dir, "absent.config.js")
	malformed := d.WriteFile("broken.config.js", "globalThis.getConfig = () => ({ apps: {\n")

	for _, tc := range []struct {
		name string
		path string
	}{
		{"missing", missing},
		{"malformed", malformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir, CacheDir: t.TempDir(), Env: sourceEnv()},
				"source", "bash", "--config", tc.path)
			if res.ExitCode == 0 {
				t.Fatalf("`source bash --config %s` exited 0:\n%s", tc.path, res.Stdout)
			}
			if res.Stdout != "" {
				t.Errorf("failure wrote to stdout, which would be eval'd:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stderr, tc.path) {
				t.Errorf("failure message does not name the config path %s:\n%s", tc.path, res.Stderr)
			}
		})
	}
}

// sourceStatusDoc is the subset of `source status --json` these tests read.
type sourceStatusDoc struct {
	Origin      string   `json:"origin"`
	Root        string   `json:"root"`
	ConfigPaths []string `json:"configPaths"`
	FarmDir     string   `json:"farmDir"`
	Entries     []struct {
		Name      string `json:"name"`
		Strategy  string `json:"strategy"`
		Installed bool   `json:"installed"`
	} `json:"entries"`
	Excluded []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"excluded"`
}

func decodeStatus(t *testing.T, out string) sourceStatusDoc {
	t.Helper()
	var doc sourceStatusDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("`source status --json` does not parse: %v\n%s", err, out)
	}
	return doc
}

// TestSourceConfigActivatesOutsideARepository is the acceptance case: the
// invocation a shell rc file carries, run where `git rev-parse` fails, emits
// valid shell code and exits 0.
func TestSourceConfigActivatesOutsideARepository(t *testing.T) {
	d, cfg := writeMachineConfig(t)
	cacheDir := t.TempDir()

	res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir, CacheDir: cacheDir, Env: sourceEnv()},
		"source", "bash", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("`source bash --config <path>` outside a repository exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}

	// A farm with no git root exports the chain that identifies it, and
	// deliberately not a root variable: an empty DATAMITSU_ROOT would read as a
	// repository that could not be determined.
	if !strings.HasPrefix(res.Stdout, "unset DATAMITSU_ROOT\nexport DATAMITSU_FARM=") {
		t.Errorf("activation does not clear the root and export the farm:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "DATAMITSU_ROOT=") {
		t.Errorf("a rootless farm exported a git root:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "DATAMITSU_FARM_CONFIG=") {
		t.Errorf("activation does not export the config chain:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, filepath.Base(cfg)) {
		t.Errorf("the exported chain does not name the config:\n%s", res.Stdout)
	}

	assignments := 0
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		if strings.HasPrefix(line, "PATH=") {
			assignments++
		}
	}
	if assignments != 1 {
		t.Errorf("activation assigned PATH %d times, want 1:\n%s", assignments, res.Stdout)
	}

	// The farm lives in the config namespace, never among the per-git-root
	// farms: the two identities are computed from different kinds of input and
	// share no directory.
	farm := farmDirFromActivation(t, res.Stdout)
	if !strings.Contains(farm, string(filepath.Separator)+"configs"+string(filepath.Separator)) {
		t.Errorf("farm %q is not in the config-farm namespace", farm)
	}
	if strings.Contains(farm, string(filepath.Separator)+"projects"+string(filepath.Separator)) {
		t.Errorf("a config farm was written into the project-farm namespace: %s", farm)
	}
	if _, err := os.Stat(filepath.Join(farm, "machine-tool")); err != nil {
		t.Errorf("the declared tool is not in the farm: %v", err)
	}
}

// TestSourceConfigFishActivatesOutsideARepository covers the shell the plan's
// example uses, whose renderer is a separate one.
func TestSourceConfigFishActivatesOutsideARepository(t *testing.T) {
	d, cfg := writeMachineConfig(t)

	res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir, CacheDir: t.TempDir(), Env: sourceEnv()},
		"source", "fish", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("`source fish --config <path>` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "fish_add_path --global --move --path ") {
		t.Errorf("fish activation does not use `fish_add_path --move`:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "set -gx DATAMITSU_FARM_CONFIG ") {
		t.Errorf("fish activation does not export the config chain:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "export ") {
		t.Errorf("fish activation contains bash syntax:\n%s", res.Stdout)
	}
}

// TestSourceConfigDownloadsNothing asserts the property the whole design rests
// on: activation is free. A machine-level farm is baked in every shell rc file
// on the machine, so a download there would be paid on every new terminal.
func TestSourceConfigDownloadsNothing(t *testing.T) {
	d, cfg := writeMachineConfig(t)
	cacheDir := t.TempDir()

	// DATAMITSU_OFFLINE=1 is already in the harness base environment; pinning it
	// here states the property this test is about rather than relying on it.
	res := clitest.Run(t, clitest.RunOptions{
		Dir: d.Dir, CacheDir: cacheDir, Env: append(sourceEnv(), "DATAMITSU_OFFLINE=1"),
	}, "source", "bash", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("`source bash --config <path>` offline exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	if store := filepath.Join(cacheDir, "store", ".bin"); dirExists(store) {
		t.Errorf("activation populated the store at %s", store)
	}

	status := clitest.Run(t, clitest.RunOptions{
		Dir: d.Dir, CacheDir: cacheDir, Env: append(sourceEnv(), "DATAMITSU_OFFLINE=1"),
	}, "source", "status", "--json", "--config", cfg)
	if status.ExitCode != 0 {
		t.Fatalf("`source status --json` exit = %d, want 0\nstderr:\n%s", status.ExitCode, status.Stderr)
	}
	doc := decodeStatus(t, status.Stdout)
	if len(doc.Entries) != 1 || doc.Entries[0].Name != "machine-tool" {
		t.Fatalf("entries = %+v, want exactly machine-tool", doc.Entries)
	}
	if doc.Entries[0].Installed || doc.Entries[0].Strategy != "shim" {
		t.Errorf("activation installed the tool: %+v", doc.Entries[0])
	}
}

// TestSourceConfigStatusReportsOrigin pins what `source status` says about a
// farm with no git root. Which origin a farm has decides what its shims do, so
// it is reported rather than left to be inferred from an empty root.
func TestSourceConfigStatusReportsOrigin(t *testing.T) {
	d, cfg := writeMachineConfig(t)
	opts := clitest.RunOptions{Dir: d.Dir, CacheDir: t.TempDir(), Env: sourceEnv()}

	res := clitest.Run(t, opts, "source", "status", "--json", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("`source status --json --config <path>` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	doc := decodeStatus(t, res.Stdout)
	if doc.Origin != "explicit-config" {
		t.Errorf("origin = %q, want explicit-config", doc.Origin)
	}
	if doc.Root != "" {
		t.Errorf("root = %q, want empty for a farm with no git root", doc.Root)
	}
	if len(doc.ConfigPaths) != 1 || filepath.Base(doc.ConfigPaths[0]) != filepath.Base(cfg) {
		t.Errorf("configPaths = %v, want the chain that was named", doc.ConfigPaths)
	}
	if !filepath.IsAbs(doc.ConfigPaths[0]) {
		t.Errorf("configPaths[0] = %q is not absolute", doc.ConfigPaths[0])
	}

	human := clitest.Run(t, opts, "source", "status", "--config", cfg)
	if human.ExitCode != 0 {
		t.Fatalf("`source status --config <path>` exit = %d, want 0\nstderr:\n%s", human.ExitCode, human.Stderr)
	}
	if !strings.Contains(human.Stdout, "origin:   explicit-config") {
		t.Errorf("the human report does not name the origin:\n%s", human.Stdout)
	}
	if !strings.Contains(human.Stdout, "config:   "+doc.ConfigPaths[0]) {
		t.Errorf("the human report does not name the config chain:\n%s", human.Stdout)
	}
	if strings.Contains(human.Stdout, "root:") {
		t.Errorf("the human report printed an empty root line:\n%s", human.Stdout)
	}
}

// TestSourceConfigExclusionsStillApply asserts the deny-list and the shell-app
// refusal are unchanged for a machine-level farm — and that both are reported
// with a reason. This matters more here than in a project: the farm is first on
// PATH in every shell, so the list of names it refused (and the ones it took
// over) is the thing the user most needs to be able to read.
func TestSourceConfigExclusionsStillApply(t *testing.T) {
	d, cfg := writeMachineConfig(t)
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: d.Dir, CacheDir: cacheDir, Env: sourceEnv()}

	res := clitest.Run(t, opts, "source", "status", "--json", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("`source status --json` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	doc := decodeStatus(t, res.Stdout)

	reasons := make(map[string]string, len(doc.Excluded))
	for _, x := range doc.Excluded {
		if strings.TrimSpace(x.Reason) == "" {
			t.Errorf("exclusion %q carries no reason", x.Name)
		}
		reasons[x.Name] = x.Reason
	}
	if _, ok := reasons["echo"]; !ok {
		t.Errorf("a shell app became a farm entry; exclusions = %+v", doc.Excluded)
	}
	if !strings.Contains(reasons["sudo"], "deny-list") {
		t.Errorf("deny-listed name reason = %q, want it to name the deny-list", reasons["sudo"])
	}

	// The refusal is structural, not cosmetic: neither name may exist in the
	// directory that goes on PATH.
	bake := clitest.Run(t, opts, "source", "bash", "--config", cfg)
	if bake.ExitCode != 0 {
		t.Fatalf("`source bash --config <path>` exit = %d, want 0\nstderr:\n%s", bake.ExitCode, bake.Stderr)
	}
	farm := farmDirFromActivation(t, bake.Stdout)
	for _, name := range []string{"echo", "sudo"} {
		if _, err := os.Lstat(filepath.Join(farm, name)); !os.IsNotExist(err) {
			t.Errorf("the farm at %s contains the excluded name %q (lstat err = %v)", farm, name, err)
		}
	}
}

// TestSourceConfigDoesNotMergeTheSurroundingRepository is the trust boundary in
// its bake form. Running the machine-level activation from inside a repository
// must produce the same farm as running it outside one: the repository's config
// is not discovered, not evaluated into the farm, and not able to add a name to
// a toolchain the user activates in every shell.
//
// Without it the farm would also be self-erasing — the shim rebakes an
// explicit-config farm with --no-auto-config, so the merged-in names would
// vanish on the first config change and every one of them would fall through
// PATH to whatever the system has.
func TestSourceConfigDoesNotMergeTheSurroundingRepository(t *testing.T) {
	_, cfg := writeMachineConfig(t)

	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js",
		"globalThis.getConfig = () => ({ apps: { \"project-only\": { shell: { name: \"true\" } } },"+
			" tools: {}, projectTypes: {} });\n"+
			"globalThis.getMinVersion = () => \"0.0.0\";\n")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: t.TempDir(), Env: sourceEnv()},
		"source", "status", "--json", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("`source status --json --config <path>` inside a repository exit = %d, want 0\nstderr:\n%s",
			res.ExitCode, res.Stderr)
	}
	doc := decodeStatus(t, res.Stdout)
	if doc.Origin != "explicit-config" || doc.Root != "" {
		t.Errorf("a --config activation inside a repository produced a %s farm rooted at %q", doc.Origin, doc.Root)
	}
	for _, e := range doc.Entries {
		if e.Name == "project-only" {
			t.Fatalf("the surrounding repository's config was merged into the machine-level farm: %+v", doc.Entries)
		}
	}
	for _, x := range doc.Excluded {
		if x.Name == "project-only" {
			t.Fatalf("the surrounding repository's config was evaluated for the machine-level farm: %+v", doc.Excluded)
		}
	}
}

// TestSourceConfigRefreshRebakesTheChain asserts `source refresh` works against
// a config farm exactly as it does against a project one: a no-op while the
// chain is unchanged, and a re-bake once it is.
func TestSourceConfigRefreshRebakesTheChain(t *testing.T) {
	d, cfg := writeMachineConfig(t)
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: d.Dir, CacheDir: cacheDir, Env: sourceEnv()}

	if res := clitest.Run(t, opts, "source", "bash", "--config", cfg); res.ExitCode != 0 {
		t.Fatalf("`source bash --config <path>` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	noop := clitest.Run(t, opts, "source", "refresh", "--config", cfg)
	if noop.ExitCode != 0 {
		t.Fatalf("`source refresh --config <path>` exit = %d, want 0\nstderr:\n%s", noop.ExitCode, noop.Stderr)
	}
	if noop.Stdout != "" {
		t.Errorf("`source refresh` wrote to stdout:\n%s", noop.Stdout)
	}
	if !strings.Contains(noop.Stderr, "already up to date") {
		t.Errorf("an unchanged chain was re-baked:\n%s", noop.Stderr)
	}
	// A farm with no git root is named by the chain a user would pass again,
	// never by a repository that does not exist.
	if !strings.Contains(noop.Stderr, cfg) {
		t.Errorf("the refresh summary does not name the config chain:\n%s", noop.Stderr)
	}

	d.WriteFile("machine.config.js", strings.ReplaceAll(machineConfigJS, "machine-tool", "renamed-tool"))

	rebake := clitest.Run(t, opts, "source", "refresh", "--config", cfg)
	if rebake.ExitCode != 0 {
		t.Fatalf("`source refresh --config <path>` after an edit exit = %d, want 0\nstderr:\n%s",
			rebake.ExitCode, rebake.Stderr)
	}
	if !strings.Contains(rebake.Stderr, "baked 1 tool(s)") {
		t.Errorf("a changed chain did not re-bake:\n%s", rebake.Stderr)
	}

	status := decodeStatus(t, clitest.Run(t, opts, "source", "status", "--json", "--config", cfg).Stdout)
	if len(status.Entries) != 1 || status.Entries[0].Name != "renamed-tool" {
		t.Errorf("entries after the re-bake = %+v, want renamed-tool", status.Entries)
	}
	if _, err := os.Stat(filepath.Join(status.FarmDir, "renamed-tool")); err != nil {
		t.Errorf("the re-baked farm does not hold the renamed tool: %v", err)
	}
}

// TestSourceConfigOneIdentityPerChain asserts the same config named three ways
// activates one farm rather than three. A shell rc file, a symlinked dotfiles
// directory and a hand-typed relative path all name the same tools, and three
// farms would mean three copies of every shim and three rebakes per edit.
func TestSourceConfigOneIdentityPerChain(t *testing.T) {
	d, cfg := writeMachineConfig(t)
	cacheDir := t.TempDir()

	link := filepath.Join(d.Dir, "linked.config.js")
	if err := os.Symlink(cfg, link); err != nil {
		t.Skipf("symlinks are unavailable here, leaving one-identity-per-chain unverified: %v", err)
	}

	farms := make(map[string]bool)
	for _, spelling := range []string{cfg, "machine.config.js", "./machine.config.js", link} {
		res := clitest.Run(t, clitest.RunOptions{Dir: d.Dir, CacheDir: cacheDir, Env: sourceEnv()},
			"source", "bash", "--config", spelling)
		if res.ExitCode != 0 {
			t.Fatalf("`source bash --config %s` exit = %d, want 0\nstderr:\n%s", spelling, res.ExitCode, res.Stderr)
		}
		farms[farmDirFromActivation(t, res.Stdout)] = true
	}
	if len(farms) != 1 {
		t.Errorf("one config produced %d farms: %v", len(farms), farms)
	}
}
