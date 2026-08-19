package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// sourceEnv pins the variables shadow detection and the renderers read. The
// clitest harness strips DATAMITSU_*, CI, TERM and NO_COLOR but deliberately
// leaves PATH and SHELL alone, and `source` reports every declared name it also
// finds on PATH — so without pinning both, these assertions pass on a laptop and
// fail on a runner that happens to have a tool installed.
func sourceEnv() []string {
	return []string{"PATH=/nonexistent-for-tests", "SHELL=/bin/sh"}
}

// writeAutoConfig writes an auto-discoverable config declaring no apps at all,
// which is what makes activation output depend on nothing but the farm path.
func writeAutoConfig(p *clitest.Project) {
	p.WriteFile("datamitsu.config.js",
		"globalThis.getConfig = () => ({ apps: {}, tools: {}, projectTypes: {} });\n"+
			"globalThis.getMinVersion = () => \"0.0.0\";\n")
}

// TestSourceBashActivation locks the shape of the bash activation: stdout is
// shell code and nothing else, and it mutates PATH exactly once.
func TestSourceBashActivation(t *testing.T) {
	p := clitest.NewProject(t)
	writeAutoConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: sourceEnv()}, "source", "bash")
	if res.ExitCode != 0 {
		t.Fatalf("`source bash` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.HasPrefix(res.Stdout, "export DATAMITSU_ROOT=") {
		t.Errorf("activation does not start with the root export:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "hash -r\n") {
		t.Errorf("activation does not flush the shell's command hash:\n%s", res.Stdout)
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
	// A config declaring no apps has nothing to warn about, so activation in a
	// shell rc file prints nothing.
	if res.Stderr != "" {
		t.Errorf("`source bash` wrote to stderr:\n%s", res.Stderr)
	}
}

// TestSourceZshActivation asserts zsh gets the same activation as bash — they
// share a renderer, and this is what would catch them silently diverging.
func TestSourceZshActivation(t *testing.T) {
	p := clitest.NewProject(t)
	writeAutoConfig(p)

	bash := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: t.TempDir(), Env: sourceEnv()}, "source", "bash")
	zsh := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: t.TempDir(), Env: sourceEnv()}, "source", "zsh")
	if zsh.ExitCode != 0 {
		t.Fatalf("`source zsh` exit = %d, want 0\nstderr:\n%s", zsh.ExitCode, zsh.Stderr)
	}
	if strings.Count(zsh.Stdout, "\n") != strings.Count(bash.Stdout, "\n") {
		t.Errorf("zsh activation differs in shape from bash:\n--- zsh ---\n%s\n--- bash ---\n%s", zsh.Stdout, bash.Stdout)
	}
	if !strings.Contains(zsh.Stdout, "hash -r\n") {
		t.Errorf("zsh activation does not flush the command hash:\n%s", zsh.Stdout)
	}
}

// TestSourceFishActivation locks fish's distinct renderer, whose --move flag is
// the difference between re-activation working and silently doing nothing.
func TestSourceFishActivation(t *testing.T) {
	p := clitest.NewProject(t)
	writeAutoConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: sourceEnv()}, "source", "fish")
	if res.ExitCode != 0 {
		t.Fatalf("`source fish` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "fish_add_path --global --move --path ") {
		t.Errorf("fish activation does not use `fish_add_path --move`:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "export ") {
		t.Errorf("fish activation contains bash syntax:\n%s", res.Stdout)
	}
}

// TestSourceStatus locks the report a user reaches for when a tool is missing or
// the wrong version: the root, the farm, the manifest's freshness, and the three
// lists. The fixture declares a shell app and a deny-listed name so both
// exclusion paths are exercised end to end.
func TestSourceStatus(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js",
		"globalThis.getConfig = () => ({ apps: {\n"+
			"  echo: { shell: { name: \"echo\" } },\n"+
			"  sudo: { shell: { name: \"sudo\" } },\n"+
			"}, tools: {}, projectTypes: {} });\n"+
			"globalThis.getMinVersion = () => \"0.0.0\";\n")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: sourceEnv()}, "source", "status")
	if res.ExitCode != 0 {
		t.Fatalf("`source status` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	for _, want := range []string{"root:", "farm:", "manifest:", "entries (", "excluded (", "shadowed (", "echo", "sudo"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("`source status` output is missing %q:\n%s", want, res.Stdout)
		}
	}
	// The farm was never baked in this project, so the report must say so
	// rather than quietly claiming a fresh farm.
	if !strings.Contains(res.Stdout, "missing") {
		t.Errorf("`source status` did not report the absent manifest:\n%s", res.Stdout)
	}
	// Every refusal carries its reason: a name that merely vanished from the
	// report would be indistinguishable from one that was never declared.
	if !strings.Contains(res.Stdout, "shell apps resolve through the inherited PATH") {
		t.Errorf("`source status` dropped the exclusion reason:\n%s", res.Stdout)
	}
}

// TestSourceStatusJSON asserts --json is a document and nothing else: it parses,
// it carries the required keys, and not a byte of the human report or of shell
// activation code leaks into it.
func TestSourceStatusJSON(t *testing.T) {
	p := clitest.NewProject(t)
	writeAutoConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: sourceEnv()}, "source", "status", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("`source status --json` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("`source status --json` stdout does not parse: %v\n%s", err, res.Stdout)
	}
	for _, key := range []string{"root", "farmDir", "manifest", "entries", "excluded"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("JSON output is missing key %q:\n%s", key, res.Stdout)
		}
	}
	for _, forbidden := range []string{"root:", "entries (", "export DATAMITSU_", "PATH="} {
		if strings.Contains(res.Stdout, forbidden) {
			t.Errorf("JSON output contains human text %q:\n%s", forbidden, res.Stdout)
		}
	}
}

// TestSourceStatusDoesNotBake pins status as a diagnostic. A command that
// repairs the farm it is describing cannot be used to observe a broken one.
func TestSourceStatusDoesNotBake(t *testing.T) {
	p := clitest.NewProject(t)
	writeAutoConfig(p)
	cacheDir := t.TempDir()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: sourceEnv()}, "source", "status")
	if res.ExitCode != 0 {
		t.Fatalf("`source status` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	var manifests []string
	_ = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "manifest.json" {
			manifests = append(manifests, path)
		}
		return nil
	})
	if len(manifests) != 0 {
		t.Errorf("`source status` baked a farm: %v", manifests)
	}
}

// TestSourceRefresh covers the whole refresh contract in one project, because
// the interesting assertions are about the transition between runs: a first bake,
// a second run that must recognize the tree as unchanged, and --force overriding
// that recognition. stdout stays empty throughout — refresh may be called from
// the same shell function that runs an activation through `eval`.
func TestSourceRefresh(t *testing.T) {
	p := clitest.NewProject(t)
	writeAutoConfig(p)
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: sourceEnv()}

	first := clitest.Run(t, opts, "source", "refresh")
	if first.ExitCode != 0 {
		t.Fatalf("first `source refresh` exit = %d, want 0\nstderr:\n%s", first.ExitCode, first.Stderr)
	}
	if first.Stdout != "" {
		t.Errorf("`source refresh` wrote to stdout:\n%s", first.Stdout)
	}
	if !strings.Contains(first.Stderr, "baked") {
		t.Errorf("first refresh did not report a bake:\n%s", first.Stderr)
	}
	if manifests := findManifests(t, cacheDir); len(manifests) != 1 {
		t.Fatalf("first refresh wrote %d manifests, want 1: %v", len(manifests), manifests)
	}

	// Nothing in the tree changed, so the farm on disk already describes it.
	// Rewriting it would churn inodes under every live shell for no change.
	second := clitest.Run(t, opts, "source", "refresh")
	if second.ExitCode != 0 {
		t.Fatalf("second `source refresh` exit = %d, want 0\nstderr:\n%s", second.ExitCode, second.Stderr)
	}
	if second.Stdout != "" {
		t.Errorf("no-op `source refresh` wrote to stdout:\n%s", second.Stdout)
	}
	if !strings.Contains(second.Stderr, "already up to date") {
		t.Errorf("second refresh re-baked an unchanged tree:\n%s", second.Stderr)
	}

	// --force is the documented escape hatch for the changes the watch set
	// cannot see: a config branching on an environment variable outside
	// datamitsu's own namespace changes what it produces without touching a
	// single watched file.
	forced := clitest.Run(t, opts, "source", "refresh", "--force")
	if forced.ExitCode != 0 {
		t.Fatalf("`source refresh --force` exit = %d, want 0\nstderr:\n%s", forced.ExitCode, forced.Stderr)
	}
	if forced.Stdout != "" {
		t.Errorf("`source refresh --force` wrote to stdout:\n%s", forced.Stdout)
	}
	if !strings.Contains(forced.Stderr, "baked") {
		t.Errorf("--force did not re-bake a fresh manifest:\n%s", forced.Stderr)
	}
}

// TestSourceRefreshDownloadsNothing asserts refresh resolves and materializes
// without fetching. The whole blackbox suite runs under DATAMITSU_OFFLINE=1, so
// a download attempt would fail the command outright; the store assertion catches
// the subtler version where something arrives from a cache instead.
func TestSourceRefreshDownloadsNothing(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js",
		"globalThis.getConfig = () => ({ apps: {\n"+
			"  shellcheck: { binary: { binaries: { linux: { amd64: { glibc: { url: \"https://example.test/x.tar.gz\", hash: \""+
			strings.Repeat("11", 32)+"\", contentType: \"tar.gz\" } } },\n"+
			"    darwin: { arm64: { unknown: { url: \"https://example.test/y.tar.gz\", hash: \""+
			strings.Repeat("22", 32)+"\", contentType: \"tar.gz\" } } } } } },\n"+
			"}, tools: {}, projectTypes: {} });\n"+
			"globalThis.getMinVersion = () => \"0.0.0\";\n")
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: sourceEnv()}

	res := clitest.Run(t, opts, "source", "refresh")
	if res.ExitCode != 0 {
		t.Fatalf("`source refresh` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	// The tool was never fetched, so it stays a shim entry that installs on its
	// first real use rather than becoming an error at activation time.
	status := clitest.Run(t, opts, "source", "status", "--json")
	var doc struct {
		Entries []struct {
			Name      string `json:"name"`
			Strategy  string `json:"strategy"`
			Installed bool   `json:"installed"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(status.Stdout), &doc); err != nil {
		t.Fatalf("`source status --json` does not parse: %v\n%s", err, status.Stdout)
	}
	if len(doc.Entries) != 1 || doc.Entries[0].Name != "shellcheck" {
		t.Fatalf("farm entries = %+v, want exactly shellcheck", doc.Entries)
	}
	if doc.Entries[0].Installed || doc.Entries[0].Strategy != "shim" {
		t.Errorf("refresh installed the tool: %+v", doc.Entries[0])
	}

	if store := filepath.Join(cacheDir, "store", ".bin"); dirExists(store) {
		t.Errorf("refresh populated the store at %s", store)
	}
}

// findManifests lists every baked farm manifest under a cache directory.
func findManifests(t *testing.T, cacheDir string) []string {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "manifest.json" {
			found = append(found, path)
		}
		return nil
	})
	return found
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// TestSourceOutsideAGitRepository asserts the loud failure. Activating against
// the embedded default config would emit shell code for a handful of built-in
// apps and look like it worked, which is the worst available outcome.
func TestSourceOutsideAGitRepository(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{Dir: t.TempDir(), Env: sourceEnv()}, "source", "bash")
	if res.ExitCode == 0 {
		t.Fatalf("`source bash` outside a repository exited 0:\n%s", res.Stdout)
	}
	if res.Stdout != "" {
		t.Errorf("`source bash` wrote to stdout on failure:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "--config") {
		t.Errorf("failure message does not name --config:\n%s", res.Stderr)
	}
}

// TestSourceWithoutAConfig is the same failure for the subtler case: a real
// repository that simply declares no config.
func TestSourceWithoutAConfig(t *testing.T) {
	p := clitest.NewProject(t)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: sourceEnv()}, "source", "bash")
	if res.ExitCode == 0 {
		t.Fatalf("`source bash` with no config exited 0:\n%s", res.Stdout)
	}
	if res.Stdout != "" {
		t.Errorf("`source bash` wrote to stdout on failure:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "--config") {
		t.Errorf("failure message does not name --config:\n%s", res.Stderr)
	}
}

// TestSourceUnknownShell asserts an unsupported shell is refused by cobra rather
// than silently emitting bash code.
func TestSourceUnknownShell(t *testing.T) {
	p := clitest.NewProject(t)
	writeAutoConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: sourceEnv()}, "source", "powershell")
	if res.ExitCode == 0 {
		t.Fatalf("`source powershell` exited 0:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "PATH=") {
		t.Errorf("`source powershell` emitted activation code:\n%s", res.Stdout)
	}
}
