package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/configcache"

	"github.com/dop251/goja"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// loadCached evaluates a standalone chain the way every non-setup command does.
// The returned VM is the hit signal: a load that evaluated returns the last
// layer's runtime, and a load served from the cache has none to return.
func loadCached(t *testing.T, paths ...string) (*config.Config, *config.SetupLayerMap, *goja.Runtime) {
	t.Helper()
	cfg, layerMap, vm, err := loadConfigWithPaths(context.Background(), nil, true, paths)
	if err != nil {
		t.Fatalf("loadConfigWithPaths(%v): %v", paths, err)
	}
	return cfg, layerMap, vm
}

func servedFromCache(vm *goja.Runtime) bool { return vm == nil }

func marshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

// The load-bearing test of the whole plan: for one chain, the config a hit
// returns must equal the config a miss returns — as a whole graph, not as a
// count of apps that happen to agree.
func TestConfigCacheHitEqualsMiss(t *testing.T) {
	cacheHome := isolateCacheTree(t)
	path := writeStandaloneConfig(t, `
		return {
			ignoreRules: ["cached: eslint"],
			tools: { eslint: { name: "eslint", operations: { lint: { app: "eslint", args: ["."] } } } },
		};`)

	cold, _, coldVM := loadCached(t, path)
	if servedFromCache(coldVM) {
		t.Fatal("the first load was served from an empty cache")
	}
	if n := configEvalArtifactCount(t, cacheHome); n != 1 {
		t.Fatalf("stored %d artifacts after the first load, want 1", n)
	}

	warm, warmLayers, warmVM := loadCached(t, path)
	if !servedFromCache(warmVM) {
		t.Fatal("the second identical load evaluated instead of hitting the cache")
	}
	if got := marshal(t, warm); got != marshal(t, cold) {
		t.Errorf("cached config differs from the evaluated one:\n hit  %s\n miss %s", got, marshal(t, cold))
	}
	if warmLayers == nil {
		t.Fatal("a hit returned a nil layer map, want an empty one")
	}
	if len(*warmLayers) != 0 {
		t.Errorf("a hit returned %d setup layers, want an empty map", len(*warmLayers))
	}
}

// The same equality over a realistic graph rather than a two-field fixture. The
// chain here carries the embedded default config forward, so apps, runtimes,
// tools and setup entries all pass through msgpack — a field the encoder
// silently drops or normalizes is what makes this cache fast and wrong, and a
// small fixture would never touch one.
func TestConfigCacheHitEqualsMissForTheDefaultChain(t *testing.T) {
	isolateCacheTree(t)
	// Object.assign, not spread: the config is loaded as plain JavaScript and
	// goja is the parser, so the test must not depend on its ES level.
	path := writeStandaloneConfig(t, `
		return Object.assign({}, input, {
			ignoreRules: ["rich: eslint"],
			tools: {
				eslint: {
					name: "eslint",
					operations: {
						lint: { app: "eslint", args: ["--format", "json", "."], scope: "project" },
						fix: { app: "eslint", args: ["--fix", "."] },
					},
				},
			},
			setup: { ".editorconfig": { linkTarget: "shared/.editorconfig" } },
			execution: { maxConcurrency: 3 },
		});`)

	cold, _, coldVM := loadCached(t, path)
	if servedFromCache(coldVM) {
		t.Fatal("the first load was served from an empty cache")
	}
	if len(cold.Apps) == 0 || len(cold.Tools) == 0 || len(cold.Setup) == 0 {
		t.Fatalf("the merged config is thin (apps %d, tools %d, setup %d); the fixture is not exercising the graph",
			len(cold.Apps), len(cold.Tools), len(cold.Setup))
	}

	warm, _, warmVM := loadCached(t, path)
	if !servedFromCache(warmVM) {
		t.Fatal("the second identical load evaluated instead of hitting the cache")
	}
	if got := marshal(t, warm); got != marshal(t, cold) {
		t.Errorf("cached config differs from the evaluated one:\n hit  %s\n miss %s", got, marshal(t, cold))
	}
}

// A second identical invocation must not rewrite the artifact: the entry is
// immutable per key, and a write on the hit path would make every command pay
// the miss path's encode.
func TestConfigCacheHitDoesNotRewriteTheArtifact(t *testing.T) {
	cacheHome := isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return { ignoreRules: ["a: eslint"] };`)

	loadCached(t, path)
	artifact := singleArtifact(t, cacheHome)
	before, err := os.Stat(artifact)
	if err != nil {
		t.Fatal(err)
	}

	_, _, vm := loadCached(t, path)
	if !servedFromCache(vm) {
		t.Fatal("the second load evaluated instead of hitting the cache")
	}
	after, err := os.Stat(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("artifact mtime moved on a hit: %v -> %v", before.ModTime(), after.ModTime())
	}
}

// An edited config must miss. This is the input the cache exists to track, so a
// key that missed it would serve the previous config forever.
func TestConfigCacheMissesWhenAConfigFileChanges(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return { ignoreRules: ["first: eslint"] };`)

	first, _, _ := loadCached(t, path)

	writeFile(t, path, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["second: eslint"] }; }`)

	second, _, vm := loadCached(t, path)
	if servedFromCache(vm) {
		t.Fatal("an edited config was served from the cache")
	}
	if marshal(t, second) == marshal(t, first) {
		t.Error("the edited config produced the previous result")
	}
}

// The environment is hashed whole, not just the DATAMITSU_* subset: the shared
// config branches on CI, and a key that ignored it would serve a CI config to a
// developer's shell.
func TestConfigCacheMissesWhenTheEnvironmentChanges(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `
		var ci = (facts().env.CI === "1");
		return { ignoreRules: [(ci ? "ci" : "local") + ": eslint"] };`)

	t.Setenv("CI", "")
	local, _, _ := loadCached(t, path)

	t.Setenv("CI", "1")
	ci, _, vm := loadCached(t, path)
	if servedFromCache(vm) {
		t.Fatal("a changed environment was served from the cache")
	}
	if marshal(t, ci) == marshal(t, local) {
		t.Error("the config did not observe the changed environment")
	}
}

// The off switch must be absolute: no read, no write, every load evaluates.
func TestConfigCacheDisabledAlwaysEvaluates(t *testing.T) {
	cacheHome := isolateCacheTree(t)
	t.Setenv("DATAMITSU_CONFIG_CACHE", "0")
	path := writeStandaloneConfig(t, `return { ignoreRules: ["off: eslint"] };`)

	for i := range 2 {
		if _, _, vm := loadCached(t, path); servedFromCache(vm) {
			t.Fatalf("load %d was served from the cache with DATAMITSU_CONFIG_CACHE=0", i)
		}
	}
	if n := configEvalArtifactCount(t, cacheHome); n != 0 {
		t.Errorf("stored %d artifacts with the cache disabled, want 0", n)
	}
}

// setup is the one caller that uses the returned VM, so it must never be served
// from the cache — and must never leave an artifact whose empty layer map a
// later setup could inherit.
func TestLoadConfigForSetupNeverServesFromCache(t *testing.T) {
	cacheHome := isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return { ignoreRules: ["setup: eslint"] };`)

	withConfigPaths(t, path)
	for i := range 2 {
		_, layerMap, vm, err := loadConfigForSetup(context.Background())
		if err != nil {
			t.Fatalf("loadConfigForSetup (load %d): %v", i, err)
		}
		if vm == nil {
			t.Fatalf("load %d returned no VM; setup cannot run without one", i)
		}
		if layerMap == nil {
			t.Fatalf("load %d returned a nil layer map", i)
		}
	}
	if n := configEvalArtifactCount(t, cacheHome); n != 0 {
		t.Errorf("setup stored %d artifacts, want 0: its layer map cannot be cached", n)
	}
}

// `config chain-hash` evaluates setup content, so it must miss even when a
// warm artifact exists — and must produce the same hashes it did before the
// cache existed.
func TestChainHashUnaffectedByTheCache(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `
		return {
			setup: {
				"eslint.config.mjs": { content: function () { return "export default [];"; } },
			},
		};`)
	withConfigPaths(t, path)

	_, coldLayerMap, _, err := loadConfigForChainHash()
	if err != nil {
		t.Fatalf("loadConfigForChainHash: %v", err)
	}
	cold := config.ChainHashes(*coldLayerMap)
	if len(cold) == 0 {
		t.Fatal("chain-hash produced no entries with a cold cache")
	}

	// Warm the cache through the ordinary path, then ask again.
	loadCached(t, path)

	_, layerMap, vm, err := loadConfigForChainHash()
	if err != nil {
		t.Fatalf("loadConfigForChainHash (warm cache): %v", err)
	}
	if vm == nil {
		t.Fatal("chain-hash was served from the cache; it needs the evaluated layer map")
	}
	warm := config.ChainHashes(*layerMap)
	if !slices.Equal(cold, warm) {
		t.Errorf("chain hashes changed with a warm cache: cold %v, warm %v", cold, warm)
	}
}

// `config lockfile` validates less than every other load, so an artifact it
// wrote would let a later strict load skip the very error the lock-file rule
// exists to raise. It must therefore neither read nor write the cache.
func TestLockfileGenLoadNeverTouchesTheCache(t *testing.T) {
	cacheHome := isolateCacheTree(t)
	// No runtimes map, so the app is validated for its missing lockFile alone.
	path := writeStandaloneConfig(t, `
		return {
			apps: {
				eslint: {
					node: { packageName: "eslint", version: "9.0.0", binPath: "node_modules/.bin/eslint" },
				},
			},
		};`)
	withConfigPaths(t, path)

	if _, _, _, err := loadConfigForLockfileGen(); err != nil {
		t.Fatalf("loadConfigForLockfileGen: %v", err)
	}
	if n := configEvalArtifactCount(t, cacheHome); n != 0 {
		t.Fatalf("the lockfile-gen load stored %d artifacts, want 0", n)
	}

	// The direction that matters: a strict load afterwards must still refuse.
	_, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{path})
	if err == nil {
		t.Fatal("a strict load after the lockfile-gen load accepted an app with no lock file")
	}
	if !strings.Contains(err.Error(), "lockFile is required") {
		t.Errorf("strict load failed for the wrong reason: %v", err)
	}
}

// A hit must print the warnings its miss printed. Otherwise a config warning
// would appear on the cold run and silently never again.
func TestConfigCacheReplaysWarningsOnAHit(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `
		return { setup: { ".prettierrc": { tools: ["no-such-tool"] } } };`)

	coldLogs := swapLoggerWithObserver(t, zapcore.WarnLevel)
	if _, _, vm := loadCached(t, path); servedFromCache(vm) {
		t.Fatal("the first load was served from an empty cache")
	}
	cold := warningMessages(coldLogs)
	if len(cold) == 0 {
		t.Fatal("the evaluated load produced no warning, so there is nothing to replay")
	}

	warmLogs := swapLoggerWithObserver(t, zapcore.WarnLevel)
	if _, _, vm := loadCached(t, path); !servedFromCache(vm) {
		t.Fatal("the second identical load evaluated instead of hitting the cache")
	}
	if warm := warningMessages(warmLogs); !slices.Equal(warm, cold) {
		t.Errorf("warnings differ between a miss and a hit:\n miss %v\n hit  %v", cold, warm)
	}
}

// A hit must restore the remote configs the chain resolved: `devtools
// verify-all` reports them, and a warm cache would otherwise report none.
func TestConfigCacheReplaysRemoteURLsOnAHit(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return { ignoreRules: ["remote: eslint"] };`)

	loadCached(t, path)
	setResolvedRemoteURLs([]string{"https://example.test/leftover.js"})

	if _, _, vm := loadCached(t, path); !servedFromCache(vm) {
		t.Fatal("the second identical load evaluated instead of hitting the cache")
	}
	resolvedRemoteURLsMu.Lock()
	got := append([]string(nil), resolvedRemoteURLs...)
	resolvedRemoteURLsMu.Unlock()
	if len(got) != 0 {
		t.Errorf("a hit left the previous load's remote URLs standing: %v", got)
	}
}

// A config edited while the chain was being evaluated must not be stamped
// fresh: the key describes bytes nobody ran, and every later invocation would
// be served a config that never existed on disk.
func TestConfigCacheRefusesAChainEditedMidEvaluation(t *testing.T) {
	cacheHome := isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return { ignoreRules: ["before: eslint"] };`)

	c := newConfigCache(context.Background(), configCacheParams{
		sources:       []configSource{{name: path, path: path}},
		explicitChain: []string{path},
		noAutoConfig:  true,
		cwd:           t.TempDir(),
	})
	if c == nil {
		t.Fatal("newConfigCache returned nil for a cacheable chain")
	}

	writeFile(t, path, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["after: eslint"] }; }`)

	c.save(&chainObservations{}, &configcache.Entry{Config: &config.Config{}})
	if n := configEvalArtifactCount(t, cacheHome); n != 0 {
		t.Errorf("stored %d artifacts for a chain edited mid-evaluation, want 0", n)
	}
}

// The same guard on the other side of the evaluation: a config edited while the
// chain was being resolved disables the cache for that load outright.
func TestConfigCacheDisabledWhenTheChainChangesWhileResolving(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return { ignoreRules: ["before: eslint"] };`)
	prior := hashConfigPaths([]string{path})

	writeFile(t, path, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["after: eslint"] }; }`)

	c := newConfigCache(context.Background(), configCacheParams{
		sources:       []configSource{{name: path, path: path}},
		explicitChain: []string{path},
		noAutoConfig:  true,
		cwd:           t.TempDir(),
		prior:         prior,
	})
	if c != nil {
		t.Error("newConfigCache returned a handle for a chain that changed while resolving")
	}
}

func TestChainMatches(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.js")
	writeFile(t, present, "// present")
	absent := filepath.Join(dir, "absent.js")

	snapshot := hashConfigPaths([]string{present, absent})

	tests := []struct {
		name  string
		chain []configcache.ChainFile
		want  bool
	}{
		{"unchanged", []configcache.ChainFile{snapshot[present], snapshot[absent]}, true},
		{"empty chain", nil, true},
		{
			name:  "path the snapshot does not cover",
			chain: []configcache.ChainFile{{Path: filepath.Join(dir, "later.js"), ContentHash: "abc", Exists: true}},
			want:  true,
		},
		{
			name:  "content changed",
			chain: []configcache.ChainFile{{Path: present, ContentHash: "different", Exists: true}},
			want:  false,
		},
		{
			name:  "file appeared",
			chain: []configcache.ChainFile{{Path: absent, ContentHash: "abc", Exists: true}},
			want:  false,
		},
		{
			name:  "file vanished",
			chain: []configcache.ChainFile{{Path: present, Exists: false}},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chainMatches(snapshot, tt.chain); got != tt.want {
				t.Errorf("chainMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

// hashConfigPaths must be a set: an empty entry is not a path, and one path
// named twice is one entry.
func TestHashConfigPathsIgnoresEmptyAndDuplicatePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.js")
	writeFile(t, path, "// one")

	got := hashConfigPaths([]string{"", path, path})
	if len(got) != 1 {
		t.Fatalf("hashConfigPaths returned %d entries, want 1: %v", len(got), got)
	}
	if entry, ok := got[path]; !ok || !entry.Exists || entry.ContentHash == "" {
		t.Errorf("entry for %q = %+v, want an existing file with a hash", path, entry)
	}
}

// warningMessages returns the observed warn-level messages in order.
func warningMessages(logs *observer.ObservedLogs) []string {
	out := make([]string, 0, logs.Len())
	for _, e := range logs.All() {
		out = append(out, e.Message)
	}
	return out
}

// withConfigPaths points the package-level flag globals at one config for the
// duration of a test, the way the CLI would.
func withConfigPaths(t *testing.T, paths ...string) {
	t.Helper()
	prevPaths, prevBefore, prevNoAuto := ConfigPaths, BeforeConfigPaths, NoAutoConfig
	ConfigPaths, BeforeConfigPaths, NoAutoConfig = paths, nil, true
	t.Cleanup(func() {
		ConfigPaths, BeforeConfigPaths, NoAutoConfig = prevPaths, prevBefore, prevNoAuto
	})
}

// singleArtifact returns the one artifact under the config-eval tree, failing
// when there is not exactly one.
func singleArtifact(t *testing.T, cacheHome string) string {
	t.Helper()
	root := filepath.Join(cacheHome, "cache", "config-eval")
	var found []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d artifacts under %s, want 1", len(found), root)
	}
	return found[0]
}

// The engine resolves the git root whether or not config discovery ran —
// computeRootPath makes it the base of tools.path.rel, so it is JS-visible under
// --no-auto-config too, and such a load still caches (a --config chain supplies
// the namespace). A key that recorded an empty git root there would serve one
// root's relative paths for another's.
func TestConfigCacheInputsRecordTheGitRootUnderNoAutoConfig(t *testing.T) {
	isolateCacheTree(t)
	root := initGitRepo(t)
	t.Chdir(root)
	cfgPath := writeStandaloneConfig(t, `return { ignoreRules: ["no-auto: eslint"] };`)

	inputs, _, err := configCacheInputs(context.Background(), configCacheParams{
		sources:       []configSource{{name: cfgPath, path: cfgPath}},
		explicitChain: []string{cfgPath},
		noAutoConfig:  true,
		gitRoot:       "", // --no-auto-config: the loader never resolves one
		cwd:           root,
	})
	if err != nil {
		t.Fatalf("configCacheInputs: %v", err)
	}

	if inputs.GitRoot != root {
		t.Errorf("GitRoot = %q, want %q: the root the engine hands to computeRootPath must be in the key", inputs.GitRoot, root)
	}
	if inputs.GitHead == "" {
		t.Error("GitHead is empty; a branch switch under --no-auto-config could not move the key")
	}
	// Discovery never ran, so the discovery-only inputs stay empty.
	if !inputs.NoAutoConfig {
		t.Error("NoAutoConfig = false, want true")
	}
	if len(inputs.AutoConfigCandidates) != 0 {
		t.Errorf("AutoConfigCandidates = %v, want none: nothing was discovered", inputs.AutoConfigCandidates)
	}
}
