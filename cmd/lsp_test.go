package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// A declared before-config that does not exist yet — a shared config in
// node_modules before the first install — fails the load but is watched: its
// arrival is the fix, and nothing else the server watches changes with it.
func TestLspLoaderWatchesAMissingBeforeConfig(t *testing.T) {
	isolateCacheTree(t)
	t.Cleanup(func() { setConfigChainFiles(nil) })
	root := setupGitRoot(t)
	wrapper := filepath.Join(root, "node_modules", "wrapper", "base.js")
	writeFile(t, filepath.Join(root, ldflags.PackageName+".config.js"), `
function getBeforeConfigs() { return [{ path: "./node_modules/wrapper/base.js" }]; }
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-auto: prettier"] }; }`)

	loader := newLspLoader()
	if loader.Watch(root) == nil {
		t.Fatal("no watch list before the first load")
	}
	_, watch, err := loader.Load(context.Background(), root)
	if err == nil {
		t.Fatal("the load succeeded without the declared before-config")
	}
	if !slices.Contains(watch, wrapper) {
		t.Errorf("watch = %v, want it to include the missing %s", watch, wrapper)
	}

	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wrapper, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-wrapper: eslint"] }; }`)
	cfg, watch, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("load after the install: %v", err)
	}
	if !slices.Contains(cfg.IgnoreRules, "from-auto: prettier") || !slices.Contains(watch, wrapper) {
		t.Errorf("ignoreRules = %v, watch = %v; want the auto config loaded and the wrapper watched", cfg.IgnoreRules, watch)
	}
}
