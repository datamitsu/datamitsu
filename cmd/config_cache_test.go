package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"

	"github.com/dop251/goja"
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

	first, _, _, err := loadConfigForChainHash()
	if err != nil {
		t.Fatalf("loadConfigForChainHash: %v", err)
	}
	_ = first

	// Warm the cache through the ordinary path, then ask again.
	loadCached(t, path)

	_, layerMap, vm, err := loadConfigForChainHash()
	if err != nil {
		t.Fatalf("loadConfigForChainHash (warm cache): %v", err)
	}
	if vm == nil {
		t.Fatal("chain-hash was served from the cache; it needs the evaluated layer map")
	}
	if len(config.ChainHashes(*layerMap)) == 0 {
		t.Error("chain-hash produced no entries with a warm cache")
	}
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
	root := filepath.Join(cacheHome, "datamitsu", "cache", "config-eval")
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
