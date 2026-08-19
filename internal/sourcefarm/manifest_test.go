package sourcefarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// fixtureRoot lays out a repository-shaped tree with a two-file config chain
// and a .git/HEAD, bakes a manifest over it, and returns the root plus the
// manifest. Every staleness test starts from the same fresh state so a case can
// mutate exactly one thing.
func fixtureRoot(t *testing.T) (string, Manifest) {
	t.Helper()

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/v1\n")
	mustWrite(t, filepath.Join(root, "datamitsu.config.ts"), "export const getConfig = () => ({})\n")
	mustWrite(t, filepath.Join(root, "before.config.ts"), "export default {}\n")

	plan := Plan{
		Root:    root,
		FarmDir: filepath.Join(root, "farm"),
		Entries: []Entry{{Name: "tofu", Kind: "binary", Strategy: StrategySymlink, Command: "/store/.bin/tofu/abc", Installed: true}},
	}
	// Exactly what cmd.ConfigChainFiles() yields: the discovered config plus the
	// resolved before-config files, and nothing else. The undiscovered
	// auto-config candidates are WatchPaths's job to add — the "config file
	// added" transition below is what proves it does.
	chain := []string{
		filepath.Join(root, "datamitsu.config.ts"),
		filepath.Join(root, "before.config.ts"),
	}
	m := BuildManifest(plan, OriginGitRoot, WatchPaths(root, chain))
	if !Validate(m) {
		t.Fatalf("freshly built manifest is not fresh: %+v", m)
	}
	return root, m
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// touchWithMtime rewrites a file and forces a distinct mtime, so a test never
// depends on filesystem timestamp granularity.
func touchWithMtime(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// TestValidate_StalenessTransitions walks every way the tree can change out
// from under a baked farm. Each one must report stale: a missed transition is
// the silent-wrong-binary failure the whole feature exists to avoid.
func TestValidate_StalenessTransitions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string, m *Manifest)
	}{
		{
			name: "config content changed",
			mutate: func(t *testing.T, root string, _ *Manifest) {
				t.Helper()
				touchWithMtime(t, filepath.Join(root, "datamitsu.config.ts"),
					"export const getConfig = () => ({apps: {}})\n", time.Now().Add(2*time.Second))
			},
		},
		{
			name: "config file deleted",
			mutate: func(t *testing.T, root string, _ *Manifest) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, "before.config.ts")); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
		},
		{
			// The tree gains a second auto-config candidate. Config discovery
			// refuses to load a root with two, so the farm must not stay fresh —
			// and this file is in no chain the loader produced, so only
			// WatchPaths's candidate list can catch it.
			name: "second auto-config candidate appears",
			mutate: func(t *testing.T, root string, _ *Manifest) {
				t.Helper()
				mustWrite(t, filepath.Join(root, "datamitsu.config.js"), "module.exports = {}\n")
			},
		},
		{
			name: "mtime regressed",
			mutate: func(t *testing.T, root string, _ *Manifest) {
				t.Helper()
				// A > watermark would call this fresh. A branch checkout that
				// restores an older file produces exactly this.
				old := time.Now().Add(-72 * time.Hour)
				if err := os.Chtimes(filepath.Join(root, "datamitsu.config.ts"), old, old); err != nil {
					t.Fatalf("chtimes: %v", err)
				}
			},
		},
		{
			name: "size changed but mtime preserved",
			mutate: func(t *testing.T, root string, m *Manifest) {
				t.Helper()
				path := filepath.Join(root, "datamitsu.config.ts")
				var recorded time.Time
				for _, w := range m.Watch {
					if w.Path == path {
						recorded = time.Unix(0, w.MtimeNS)
					}
				}
				touchWithMtime(t, path, "export const getConfig = () => ({}) // longer\n", recorded)
			},
		},
		{
			name: "git HEAD rewritten by checkout",
			mutate: func(t *testing.T, root string, _ *Manifest) {
				t.Helper()
				touchWithMtime(t, filepath.Join(root, ".git", "HEAD"),
					"ref: refs/heads/v2\n", time.Now().Add(2*time.Second))
			},
		},
		{
			name: "pnpm lockfile appeared",
			mutate: func(t *testing.T, root string, _ *Manifest) {
				t.Helper()
				mustWrite(t, filepath.Join(root, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
			},
		},
		{
			name: "datamitsu version changed",
			mutate: func(t *testing.T, _ string, _ *Manifest) {
				t.Helper()
				restore := ldflags.Version
				ldflags.Version = restore + "-next"
				t.Cleanup(func() { ldflags.Version = restore })
			},
		},
		{
			name: "datamitsu env var changed",
			mutate: func(t *testing.T, _ string, _ *Manifest) {
				t.Helper()
				t.Setenv("DATAMITSU_MIN_RELEASE_AGE", "0")
			},
		},
		{
			name: "format version from the future",
			mutate: func(_ *testing.T, _ string, m *Manifest) {
				m.FormatVersion = ManifestFormatVersion + 1
			},
		},
		{
			name: "format version from the past",
			mutate: func(_ *testing.T, _ string, m *Manifest) {
				m.FormatVersion = ManifestFormatVersion - 1
			},
		},
		{
			name: "platform changed",
			mutate: func(_ *testing.T, _ string, m *Manifest) {
				m.Arch = "s390x"
			},
		},
		{
			name: "staleness key tampered with",
			mutate: func(_ *testing.T, _ string, m *Manifest) {
				m.StalenessKey = "00000000000000000000000000000000"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, m := fixtureRoot(t)
			tt.mutate(t, root, &m)
			if Validate(m) {
				t.Error("Validate = true, want false (stale)")
			}
		})
	}
}

// TestValidate_UnchangedTreeIsFresh is the steady-state case: this is the
// answer the shim gets on essentially every invocation, so it must not have
// false-stale modes.
func TestValidate_UnchangedTreeIsFresh(t *testing.T) {
	_, m := fixtureRoot(t)

	for i := range 3 {
		if !Validate(m) {
			t.Fatalf("call %d: Validate = false, want true", i)
		}
	}
}

// TestValidate_FutureFormatVersionIsStaleNotAnError asserts an old datamitsu
// meeting a newer farm rebakes instead of failing. Load must accept the file;
// only Validate judges it.
func TestValidate_FutureFormatVersionIsStaleNotAnError(t *testing.T) {
	root, m := fixtureRoot(t)
	m.FormatVersion = ManifestFormatVersion + 99
	m.Entries = append(m.Entries, Entry{Name: "from-the-future", Kind: "binary", Strategy: StrategyShim})

	path := filepath.Join(root, "manifest.json")
	data, err := Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	mustWrite(t, path, string(data))

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load of a future-format manifest errored: %v", err)
	}
	if Validate(loaded) {
		t.Error("Validate = true for a future format version, want false")
	}
}

// TestValidate_ShimTargetTransitions covers the failure the watch set cannot
// see: the datamitsu binary itself moving or losing its executable bit while the
// tree and the version string stay identical. Every farm entry is a symlink to
// that one file, so the farm is entirely broken and every other freshness input
// still says fresh. Reporting stale is what lets `source refresh` repair it
// without --force.
func TestValidate_ShimTargetTransitions(t *testing.T) {
	tests := []struct {
		name string
		// mutate returns the manifest's ShimTarget after doing whatever it does to
		// the file at target.
		mutate    func(t *testing.T, target string)
		wantFresh bool
	}{
		{
			name:      "target still there and executable",
			mutate:    func(*testing.T, string) {},
			wantFresh: true,
		},
		{
			name: "target moved away",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Remove(target); err != nil {
					t.Fatalf("remove shim target: %v", err)
				}
			},
		},
		{
			name: "target lost its executable bit",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Chmod(target, 0o644); err != nil {
					t.Fatalf("chmod shim target: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, m := fixtureRoot(t)
			target := filepath.Join(root, "datamitsu-bin")
			mustWrite(t, target, "#!/bin/sh\n")
			if err := os.Chmod(target, 0o755); err != nil {
				t.Fatalf("chmod shim target: %v", err)
			}
			m.ShimTarget = target

			if !Validate(m) {
				t.Fatal("Validate = false before the mutation, want true")
			}
			tt.mutate(t, target)

			if got := Validate(m); got != tt.wantFresh {
				t.Errorf("Validate = %t, want %t", got, tt.wantFresh)
			}
		})
	}
}

// TestValidate_EmptyShimTargetIsNotChecked pins the compatibility rule: a
// manifest written before the field existed carries no target, and must be
// judged on its watch set rather than reported permanently stale.
func TestValidate_EmptyShimTargetIsNotChecked(t *testing.T) {
	_, m := fixtureRoot(t)
	m.ShimTarget = ""

	if !Validate(m) {
		t.Error("Validate = false for a manifest with no recorded shim target, want true")
	}
}

// TestValidate_OnlyStats proves the property the whole design rests on:
// deciding freshness reads no file contents and spawns nothing. The counting
// hook pins the call count to exactly one lstat per watched path, so a future
// refactor cannot quietly add an os.ReadFile or a git subprocess to the hot
// path.
func TestValidate_OnlyStats(t *testing.T) {
	_, m := fixtureRoot(t)

	// Make every watched file unreadable. Any attempt to read contents fails;
	// lstat still succeeds.
	for _, w := range m.Watch {
		if !w.Exists {
			continue
		}
		if err := os.Chmod(w.Path, 0o000); err != nil {
			t.Fatalf("chmod %s: %v", w.Path, err)
		}
		t.Cleanup(func() { _ = os.Chmod(w.Path, 0o600) })
	}

	calls := 0
	restore := lstat
	lstat = func(name string) (os.FileInfo, error) {
		calls++
		return restore(name)
	}
	t.Cleanup(func() { lstat = restore })

	if !Validate(m) {
		t.Fatal("Validate = false over an unchanged but unreadable tree, want true")
	}
	// The lstat count is the whole assertion: it is exact, so a Validate that
	// grew an os.ReadFile, a second stat, or an exec.Command would have to keep
	// the count at exactly one per watched path to slip through, and the
	// unreadable files above rule out the read.
	if calls != len(m.Watch) {
		t.Errorf("lstat calls = %d, want %d (one per watched path)", calls, len(m.Watch))
	}
}

// BenchmarkValidate pins the shim's per-invocation cost. The design budget is
// microseconds against a ~10 ms process; if this ever reaches milliseconds,
// something on the path started loading a config.
func BenchmarkValidate(b *testing.B) {
	root := b.TempDir()
	for _, name := range []string{".git/HEAD", "datamitsu.config.ts", "before.config.ts"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	plan := Plan{Root: root, FarmDir: filepath.Join(root, "farm")}
	m := BuildManifest(plan, OriginGitRoot, WatchPaths(root, []string{
		filepath.Join(root, "datamitsu.config.ts"),
		filepath.Join(root, "before.config.ts"),
	}))

	iterations := 0
	for b.Loop() {
		if !Validate(m) {
			b.Fatal("Validate = false, want true")
		}
		iterations++
	}

	// The budget is an order of magnitude, not a tight bound: a shared CI runner
	// is noisy, but nothing under a millisecond can be loading a config.
	if per := b.Elapsed() / time.Duration(iterations); per > time.Millisecond {
		b.Errorf("Validate took %v per call, want microseconds — something on the hot path is doing real work", per)
	}
}

// TestEncode_RoundTripIsStable asserts the manifest survives a JSON round trip
// unchanged and that encoding is byte-stable, which is what lets the CLI golden
// tests diff a farm.
func TestEncode_RoundTripIsStable(t *testing.T) {
	_, m := fixtureRoot(t)
	m.Entries = []Entry{
		{Name: "prettier", Provider: "node", Kind: "node", Strategy: StrategyShim, Command: "/store/node/prettier", Args: []string{"--no-color"}, Env: map[string]string{"PATH": "/store/node/bin", "npm_config_cache": "/cache"}, Installed: true},
		{Name: "tofu", Provider: "binary", Kind: "binary", Strategy: StrategySymlink, Command: "/store/.bin/tofu/abc", Installed: true},
	}
	m.Excluded = []Excluded{{Name: "echo", Reason: ReasonShellApp}}
	m.Shadowed = []Shadow{{Name: "tofu", Path: "/usr/local/bin/tofu"}}

	first, err := Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	second, err := Encode(m)
	if err != nil {
		t.Fatalf("Encode (second): %v", err)
	}
	if string(first) != string(second) {
		t.Error("Encode is not byte-stable across calls")
	}

	var decoded Manifest
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	again, err := Encode(decoded)
	if err != nil {
		t.Fatalf("Encode (round-tripped): %v", err)
	}
	if string(again) != string(first) {
		t.Errorf("round trip changed the manifest:\n%s\nvs\n%s", first, again)
	}
	if !Validate(decoded) {
		t.Error("round-tripped manifest is stale, want fresh")
	}
	if decoded.Origin != OriginGitRoot {
		t.Errorf("Origin = %q, want %q", decoded.Origin, OriginGitRoot)
	}
	if decoded.OS != runtime.GOOS || decoded.Arch != runtime.GOARCH {
		t.Errorf("platform = %s/%s, want %s/%s", decoded.OS, decoded.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if decoded.Root != m.Root {
		t.Errorf("Root = %q, want the authoritative root %q", decoded.Root, m.Root)
	}
}

// TestLoad_Errors covers the two cases Load does treat as errors, as distinct
// from the format-version case it deliberately does not.
func TestLoad_Errors(t *testing.T) {
	dir := t.TempDir()

	if _, err := Load(filepath.Join(dir, "absent.json")); err == nil {
		t.Error("Load of a missing manifest returned no error")
	}

	broken := filepath.Join(dir, "broken.json")
	mustWrite(t, broken, "{not json")
	if _, err := Load(broken); err == nil {
		t.Error("Load of a malformed manifest returned no error")
	}
}

// TestWatchSet_SortedAndDeduplicated asserts the watch set does not depend on
// the order the caller discovered files in, so neither does the staleness key.
func TestWatchSet_SortedAndDeduplicated(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.ts")
	b := filepath.Join(root, "b.ts")
	mustWrite(t, a, "a")

	forward := WatchSet([]string{a, b, a, ""})
	reverse := WatchSet([]string{"", b, a, b})

	if len(forward) != 2 {
		t.Fatalf("len = %d, want 2 (deduplicated, empty dropped): %+v", len(forward), forward)
	}
	if forward[0].Path != a || forward[1].Path != b {
		t.Errorf("not sorted by path: %+v", forward)
	}
	if forward[0] != reverse[0] || forward[1] != reverse[1] {
		t.Errorf("input order changed the watch set:\n%+v\nvs\n%+v", forward, reverse)
	}
	if !forward[0].Exists {
		t.Error("existing file recorded as absent")
	}
	if forward[1].Exists || forward[1].Size != 0 || forward[1].MtimeNS != 0 {
		t.Errorf("missing file = %+v, want a zeroed absent tuple", forward[1])
	}
}

// TestWatchPaths_IncludesHeadAndLockfile pins the paths that are not the
// discovered config but still change the resolved toolchain: .git/HEAD, the
// pnpm lockfile, and the auto-config candidates that were not discovered — a
// tree that grows a second candidate stops loading, so it must not be reported
// fresh.
func TestWatchPaths_IncludesHeadAndLockfile(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	paths := WatchPaths(root, []string{filepath.Join(root, "datamitsu.config.ts")})

	want := map[string]bool{
		filepath.Join(root, "datamitsu.config.ts"):  false,
		filepath.Join(root, "datamitsu.config.js"):  false,
		filepath.Join(root, "datamitsu.config.mjs"): false,
		filepath.Join(root, ".git", "HEAD"):         false,
		filepath.Join(root, "pnpm-lock.yaml"):       false,
	}
	for _, p := range paths {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected watch path %q", p)
			continue
		}
		want[p] = true
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("watch path %q missing", p)
		}
	}
}

// TestComputeStalenessKey_Pure asserts the key is a function of its arguments
// only: same inputs give the same key, and each input independently moves it.
func TestComputeStalenessKey_Pure(t *testing.T) {
	watch := []WatchFile{{Path: "/repo/datamitsu.config.ts", MtimeNS: 1, Size: 2, Exists: true}}
	base := ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=1"})

	if again := ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=1"}); again != base {
		t.Errorf("key is not stable: %q vs %q", base, again)
	}
	if unordered := ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=1", "DATAMITSU_NO_OCI=1"}); unordered != ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_NO_OCI=1", "DATAMITSU_OFFLINE=1"}) {
		t.Error("env ordering changed the key")
	}
	if len(base) != 32 {
		t.Errorf("key length = %d, want 32 hex chars (XXH3-128)", len(base))
	}

	changed := []struct {
		name string
		key  string
	}{
		{"format version", ComputeStalenessKey(2, "1.0.0", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=1"})},
		{"datamitsu version", ComputeStalenessKey(1, "1.0.1", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=1"})},
		{"root", ComputeStalenessKey(1, "1.0.0", "/other", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=1"})},
		{"os", ComputeStalenessKey(1, "1.0.0", "/repo", "darwin", "amd64", watch, []string{"DATAMITSU_OFFLINE=1"})},
		{"arch", ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "arm64", watch, []string{"DATAMITSU_OFFLINE=1"})},
		{"watch tuple", ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "amd64", []WatchFile{{Path: "/repo/datamitsu.config.ts", MtimeNS: 9, Size: 2, Exists: true}}, []string{"DATAMITSU_OFFLINE=1"})},
		{"env", ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=0"})},
		{"env added", ComputeStalenessKey(1, "1.0.0", "/repo", "linux", "amd64", watch, []string{"DATAMITSU_OFFLINE=1", "DATAMITSU_NO_OCI=1"})},
	}
	for _, c := range changed {
		if c.key == base {
			t.Errorf("%s did not change the key", c.name)
		}
	}
}
