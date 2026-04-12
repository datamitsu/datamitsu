package cmd

import (
	"crypto/sha256"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/engine"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestLoadConfig(t *testing.T) {
	cfg, _, vm, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg == nil {
		t.Error("loadConfig() returned nil config")
	}

	if vm == nil {
		t.Error("loadConfig() returned nil VM")
	}

	if cfg != nil && cfg.ProjectTypes == nil {
		t.Error("config.ProjectTypes is nil")
	}

	if cfg != nil && cfg.Apps == nil {
		t.Error("config.Apps is nil")
	}

	if cfg != nil && cfg.Tools == nil {
		t.Error("config.Tools is nil")
	}
}

func TestLoadConfigRuntimes(t *testing.T) {
	cfg, _, _, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.Runtimes == nil {
		t.Fatal("config.Runtimes is nil")
	}

	uvRuntime, ok := cfg.Runtimes["uv"]
	if !ok {
		t.Fatal("uv runtime not found in config")
	}
	if uvRuntime.Kind != config.RuntimeKindUV {
		t.Errorf("uv runtime kind = %q, want %q", uvRuntime.Kind, config.RuntimeKindUV)
	}
	if uvRuntime.Mode != config.RuntimeModeManaged {
		t.Errorf("uv runtime mode = %q, want %q", uvRuntime.Mode, config.RuntimeModeManaged)
	}
	if uvRuntime.Managed == nil {
		t.Fatal("uv runtime managed config is nil")
	}
	if _, ok := uvRuntime.Managed.Binaries["linux"]; !ok {
		t.Error("uv runtime missing linux binaries")
	}
	if _, ok := uvRuntime.Managed.Binaries["darwin"]; !ok {
		t.Error("uv runtime missing darwin binaries")
	}

	fnmRuntime, ok := cfg.Runtimes["fnm"]
	if !ok {
		t.Fatal("fnm runtime not found in config")
	}
	if fnmRuntime.Kind != config.RuntimeKindFNM {
		t.Errorf("fnm runtime kind = %q, want %q", fnmRuntime.Kind, config.RuntimeKindFNM)
	}
	if fnmRuntime.Mode != config.RuntimeModeManaged {
		t.Errorf("fnm runtime mode = %q, want %q", fnmRuntime.Mode, config.RuntimeModeManaged)
	}
	if fnmRuntime.Managed == nil {
		t.Fatal("fnm runtime managed config is nil")
	}
	if _, ok := fnmRuntime.Managed.Binaries["linux"]; !ok {
		t.Error("fnm runtime missing linux binaries")
	}
	if _, ok := fnmRuntime.Managed.Binaries["darwin"]; !ok {
		t.Error("fnm runtime missing darwin binaries")
	}
}

func TestLoadConfigRuntimeApps(t *testing.T) {
	cfg, _, _, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	// Test UV app
	pycowsay, ok := cfg.Apps["pycowsay"]
	if !ok {
		t.Fatal("pycowsay app not found")
	}
	if pycowsay.Uv == nil {
		t.Fatal("pycowsay.Uv is nil")
	}
	if pycowsay.Uv.PackageName != "pycowsay" {
		t.Errorf("pycowsay packageName = %q, want %q", pycowsay.Uv.PackageName, "pycowsay")
	}
	if pycowsay.Uv.Version != "0.0.0.2" {
		t.Errorf("pycowsay version = %q, want %q", pycowsay.Uv.Version, "0.0.0.2")
	}

	// Test FNM app
	jscowsay, ok := cfg.Apps["jscowsay"]
	if !ok {
		t.Fatal("jscowsay app not found")
	}
	if jscowsay.Fnm == nil {
		t.Fatal("jscowsay.Fnm is nil")
	}
	if jscowsay.Fnm.PackageName != "cowsay" {
		t.Errorf("jscowsay packageName = %q, want %q", jscowsay.Fnm.PackageName, "cowsay")
	}
	if jscowsay.Fnm.BinPath == "" {
		t.Error("jscowsay.Fnm.BinPath is empty")
	}

	// Test JVM app
	ktlint, ok := cfg.Apps["ktlint"]
	if !ok {
		t.Fatal("ktlint app not found")
	}
	if ktlint.Jvm == nil {
		t.Fatal("ktlint.Jvm is nil")
	}
}

func newTestVM() *goja.Runtime {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	return vm
}

func TestParseConfigResultLinkTarget(t *testing.T) {
	vm := newTestVM()

	_, err := vm.RunString(`
		var result = {
			init: {
				"CLAUDE.md": {
					scope: "git-root",
					linkTarget: "AGENTS.md"
				},
				".cursorrules": {
					scope: "git-root",
					linkTarget: "AGENTS.md"
				}
			}
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}

	resultVal := vm.Get("result")
	cfg, err := parseConfigResult(vm, resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	claudeInit, ok := cfg.Init["CLAUDE.md"]
	if !ok {
		t.Fatal("CLAUDE.md init config not found")
	}
	if claudeInit.LinkTarget != "AGENTS.md" {
		t.Errorf("CLAUDE.md LinkTarget = %q, want %q", claudeInit.LinkTarget, "AGENTS.md")
	}
	if claudeInit.Scope != "git-root" {
		t.Errorf("CLAUDE.md Scope = %q, want %q", claudeInit.Scope, "git-root")
	}

	cursorInit, ok := cfg.Init[".cursorrules"]
	if !ok {
		t.Fatal(".cursorrules init config not found")
	}
	if cursorInit.LinkTarget != "AGENTS.md" {
		t.Errorf(".cursorrules LinkTarget = %q, want %q", cursorInit.LinkTarget, "AGENTS.md")
	}
}

func TestParseConfigResultLinkTargetWithRelativePath(t *testing.T) {
	vm := newTestVM()

	_, err := vm.RunString(`
		var result = {
			init: {
				".cursor/rules": {
					scope: "git-root",
					linkTarget: "../AGENTS.md"
				}
			}
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}

	resultVal := vm.Get("result")
	cfg, err := parseConfigResult(vm, resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	cursorInit, ok := cfg.Init[".cursor/rules"]
	if !ok {
		t.Fatal(".cursor/rules init config not found")
	}
	if cursorInit.LinkTarget != "../AGENTS.md" {
		t.Errorf(".cursor/rules LinkTarget = %q, want %q", cursorInit.LinkTarget, "../AGENTS.md")
	}
}

func TestParseConfigResultLinkTargetNotSet(t *testing.T) {
	vm := newTestVM()

	_, err := vm.RunString(`
		var result = {
			init: {
				".gitignore": {
					scope: "git-root",
					content: function(ctx) { return "node_modules/"; }
				}
			}
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}

	resultVal := vm.Get("result")
	cfg, err := parseConfigResult(vm, resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	gitignoreInit, ok := cfg.Init[".gitignore"]
	if !ok {
		t.Fatal(".gitignore init config not found")
	}
	if gitignoreInit.LinkTarget != "" {
		t.Errorf(".gitignore LinkTarget = %q, want empty string", gitignoreInit.LinkTarget)
	}
	if gitignoreInit.Content == nil {
		t.Error(".gitignore Content should not be nil")
	}
}

func TestParseConfigResultLinkTargetWithContent(t *testing.T) {
	vm := newTestVM()

	_, err := vm.RunString(`
		var result = {
			init: {
				"CLAUDE.md": {
					scope: "git-root",
					linkTarget: "AGENTS.md",
					content: function(ctx) { return "fallback"; }
				}
			}
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}

	resultVal := vm.Get("result")
	cfg, err := parseConfigResult(vm, resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	claudeInit := cfg.Init["CLAUDE.md"]
	if claudeInit.LinkTarget != "AGENTS.md" {
		t.Errorf("LinkTarget = %q, want %q", claudeInit.LinkTarget, "AGENTS.md")
	}
	if claudeInit.Content == nil {
		t.Error("Content should still be preserved even when linkTarget is set")
	}
}

func TestParseConfigResultInitConfigPreservesAllFields(t *testing.T) {
	vm := newTestVM()

	_, err := vm.RunString(`
		var result = {
			init: {
				"test.json": {
					projectTypes: ["node"],
					scope: "git-root",
					deleteOnly: false,
					otherFileNameList: ["test.yaml", "test.yml"],
					linkTarget: "some-target"
				}
			}
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}

	resultVal := vm.Get("result")
	cfg, err := parseConfigResult(vm, resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	testInit := cfg.Init["test.json"]
	if len(testInit.ProjectTypes) != 1 || testInit.ProjectTypes[0] != "node" {
		t.Errorf("ProjectTypes = %v, want [node]", testInit.ProjectTypes)
	}
	if testInit.Scope != "git-root" {
		t.Errorf("Scope = %q, want %q", testInit.Scope, "git-root")
	}
	if testInit.DeleteOnly {
		t.Error("DeleteOnly should be false")
	}
	if len(testInit.OtherFileNameList) != 2 {
		t.Errorf("OtherFileNameList len = %d, want 2", len(testInit.OtherFileNameList))
	}
	if testInit.LinkTarget != "some-target" {
		t.Errorf("LinkTarget = %q, want %q", testInit.LinkTarget, "some-target")
	}
}

func TestParseConfigResultIgnoreRules(t *testing.T) {
	vm := newTestVM()

	_, err := vm.RunString(`
		var result = {
			ignoreRules: [
				"**/*.generated.ts: eslint, prettier",
				"vendor/**: *"
			]
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}

	resultVal := vm.Get("result")
	cfg, err := parseConfigResult(vm, resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	if len(cfg.IgnoreRules) != 2 {
		t.Fatalf("len(IgnoreRules) = %d, want 2", len(cfg.IgnoreRules))
	}
	if cfg.IgnoreRules[0] != "**/*.generated.ts: eslint, prettier" {
		t.Errorf("IgnoreRules[0] = %q, want %q", cfg.IgnoreRules[0], "**/*.generated.ts: eslint, prettier")
	}
	if cfg.IgnoreRules[1] != "vendor/**: *" {
		t.Errorf("IgnoreRules[1] = %q, want %q", cfg.IgnoreRules[1], "vendor/**: *")
	}
}

func TestParseConfigResultIgnoreRulesEmpty(t *testing.T) {
	vm := newTestVM()

	_, err := vm.RunString(`var result = {};`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}

	resultVal := vm.Get("result")
	cfg, err := parseConfigResult(vm, resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	if len(cfg.IgnoreRules) != 0 {
		t.Errorf("len(IgnoreRules) = %d, want 0", len(cfg.IgnoreRules))
	}
}

func TestIgnoreRulesMergeAppend(t *testing.T) {
	// Simulate two config sources being merged via loadConfigWithPaths logic:
	// Previous config has ignoreRules A, new config has ignoreRules B.
	// Result should be A + B (append).

	vm1 := newTestVM()
	_, err := vm1.RunString(`
		var result1 = {
			ignoreRules: ["**/*.md: eslint"]
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}
	cfg1, err := parseConfigResult(vm1, vm1.Get("result1"))
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	vm2 := newTestVM()
	_, err = vm2.RunString(`
		var result2 = {
			ignoreRules: ["vendor/**: *", "!docs/**/*.md: eslint"]
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}
	cfg2, err := parseConfigResult(vm2, vm2.Get("result2"))
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	// Simulate the merge logic from loadConfigWithPaths
	if len(cfg1.IgnoreRules) > 0 {
		cfg2.IgnoreRules = append(cfg1.IgnoreRules, cfg2.IgnoreRules...)
	}

	if len(cfg2.IgnoreRules) != 3 {
		t.Fatalf("len(IgnoreRules) = %d, want 3", len(cfg2.IgnoreRules))
	}
	// Previous rules come first
	if cfg2.IgnoreRules[0] != "**/*.md: eslint" {
		t.Errorf("IgnoreRules[0] = %q, want %q", cfg2.IgnoreRules[0], "**/*.md: eslint")
	}
	// New rules follow
	if cfg2.IgnoreRules[1] != "vendor/**: *" {
		t.Errorf("IgnoreRules[1] = %q, want %q", cfg2.IgnoreRules[1], "vendor/**: *")
	}
	if cfg2.IgnoreRules[2] != "!docs/**/*.md: eslint" {
		t.Errorf("IgnoreRules[2] = %q, want %q", cfg2.IgnoreRules[2], "!docs/**/*.md: eslint")
	}
}

func TestIgnoreRulesMergeWithEmptyPrevious(t *testing.T) {
	vm1 := newTestVM()
	_, err := vm1.RunString(`var result1 = {};`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}
	cfg1, err := parseConfigResult(vm1, vm1.Get("result1"))
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	vm2 := newTestVM()
	_, err = vm2.RunString(`
		var result2 = {
			ignoreRules: ["**/*.md: eslint"]
		};
	`)
	if err != nil {
		t.Fatalf("JS setup error: %v", err)
	}
	cfg2, err := parseConfigResult(vm2, vm2.Get("result2"))
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}

	if len(cfg1.IgnoreRules) > 0 {
		cfg2.IgnoreRules = append(cfg1.IgnoreRules, cfg2.IgnoreRules...)
	}

	if len(cfg2.IgnoreRules) != 1 {
		t.Fatalf("len(IgnoreRules) = %d, want 1", len(cfg2.IgnoreRules))
	}
	if cfg2.IgnoreRules[0] != "**/*.md: eslint" {
		t.Errorf("IgnoreRules[0] = %q, want %q", cfg2.IgnoreRules[0], "**/*.md: eslint")
	}
}

func TestDiscoverAutoConfigOnlyTS(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, ldflags.PackageName+".config.ts")
	if err := os.WriteFile(tsPath, []byte("// ts config"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := discoverAutoConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != tsPath {
		t.Errorf("got %q, want %q", got, tsPath)
	}
}

func TestDiscoverAutoConfigOnlyJS(t *testing.T) {
	dir := t.TempDir()
	jsPath := filepath.Join(dir, ldflags.PackageName+".config.js")
	if err := os.WriteFile(jsPath, []byte("// js config"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := discoverAutoConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != jsPath {
		t.Errorf("got %q, want %q", got, jsPath)
	}
}

func TestDiscoverAutoConfigOnlyMJS(t *testing.T) {
	dir := t.TempDir()
	mjsPath := filepath.Join(dir, ldflags.PackageName+".config.mjs")
	if err := os.WriteFile(mjsPath, []byte("// mjs config"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := discoverAutoConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mjsPath {
		t.Errorf("got %q, want %q", got, mjsPath)
	}
}

func TestDiscoverAutoConfigBothExist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ldflags.PackageName+".config.ts"), []byte("// ts"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ldflags.PackageName+".config.js"), []byte("// js"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := discoverAutoConfig(dir)
	if err == nil {
		t.Fatal("expected error when multiple config files exist")
	}
	if !strings.Contains(err.Error(), "remove all but one") {
		t.Errorf("error = %q, want it to contain 'remove all but one'", err.Error())
	}
}

func TestDiscoverAutoConfigNeitherExists(t *testing.T) {
	dir := t.TempDir()

	got, err := discoverAutoConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func computeHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(h[:])
}

func TestProcessConfigSourceRemoteConfig(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	remoteContent := `function getConfig(input) { return { ignoreRules: ["from-remote: eslint"] }; }`
	remoteHash := computeHash(remoteContent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteContent))
	}))
	defer server.Close()

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/remote.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["from-local: prettier"] };
}`, server.URL, remoteHash)

	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-local",
		content: localContent,
	}, resolved, stack)
	if err != nil {
		t.Fatalf("processConfigSource error: %v", err)
	}

	if len(result.IgnoreRules) != 2 {
		t.Fatalf("len(IgnoreRules) = %d, want 2; got %v", len(result.IgnoreRules), result.IgnoreRules)
	}
	if result.IgnoreRules[0] != "from-remote: eslint" {
		t.Errorf("IgnoreRules[0] = %q, want %q", result.IgnoreRules[0], "from-remote: eslint")
	}
	if result.IgnoreRules[1] != "from-local: prettier" {
		t.Errorf("IgnoreRules[1] = %q, want %q", result.IgnoreRules[1], "from-local: prettier")
	}
}

func TestProcessConfigSourceRecursiveRemote(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	remoteBContent := `function getConfig(input) { return { ignoreRules: ["from-B: eslint"] }; }`
	remoteBHash := computeHash(remoteBContent)

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	remoteAContent := fmt.Sprintf(`
function getRemoteConfigs() {
    return [{ url: "%s/b.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["from-A: prettier"] };
}`, server.URL, remoteBHash)
	remoteAHash := computeHash(remoteAContent)

	mux.HandleFunc("/a.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteAContent))
	})
	mux.HandleFunc("/b.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteBContent))
	})

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/a.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["from-local: hadolint"] };
}`, server.URL, remoteAHash)

	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-local",
		content: localContent,
	}, resolved, stack)
	if err != nil {
		t.Fatalf("processConfigSource error: %v", err)
	}

	// Depth-first: B -> A -> local
	want := []string{"from-B: eslint", "from-A: prettier", "from-local: hadolint"}
	if len(result.IgnoreRules) != len(want) {
		t.Fatalf("len(IgnoreRules) = %d, want %d; got %v", len(result.IgnoreRules), len(want), result.IgnoreRules)
	}
	for i, w := range want {
		if result.IgnoreRules[i] != w {
			t.Errorf("IgnoreRules[%d] = %q, want %q", i, result.IgnoreRules[i], w)
		}
	}
}

func TestProcessConfigSourceRemoteMissingHash(t *testing.T) {
	localContent := `
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "http://example.com/remote.ts", hash: "" }];
}
function getConfig(input) {
    return {};
}`

	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-missing-hash",
		content: localContent,
	}, resolved, stack)
	if err == nil {
		t.Fatal("expected error for missing hash")
	}
	if !strings.Contains(err.Error(), "hash is required") {
		t.Errorf("error = %q, want it to contain 'hash is required'", err.Error())
	}
}

func TestProcessConfigSourceCircularDependency(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	// A references B, B references A (circular)
	remoteBContent := fmt.Sprintf(`
function getRemoteConfigs() {
    return [{ url: "%s/a.ts", hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000" }];
}
function getConfig(input) { return {}; }`, server.URL)
	remoteBHash := computeHash(remoteBContent)

	remoteAContent := fmt.Sprintf(`
function getRemoteConfigs() {
    return [{ url: "%s/b.ts", hash: "%s" }];
}
function getConfig(input) { return {}; }`, server.URL, remoteBHash)
	remoteAHash := computeHash(remoteAContent)

	mux.HandleFunc("/a.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteAContent))
	})
	mux.HandleFunc("/b.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteBContent))
	})

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/a.ts", hash: "%s" }];
}
function getConfig(input) { return {}; }`, server.URL, remoteAHash)

	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-circular",
		content: localContent,
	}, resolved, stack)
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
	if !strings.Contains(err.Error(), "circular remote config dependency") {
		t.Errorf("error = %q, want it to contain 'circular remote config dependency'", err.Error())
	}
}

func TestProcessConfigSourceDiamondDependency(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	// Diamond: local -> [A, B], A -> D, B -> D
	// D is a shared dependency — should be processed twice (not a cycle).
	remoteDContent := `function getConfig(input) { return { ignoreRules: ["from-D: eslint"] }; }`
	remoteDHash := computeHash(remoteDContent)

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	remoteAContent := fmt.Sprintf(`
function getRemoteConfigs() {
    return [{ url: "%s/d.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["from-A: prettier"] };
}`, server.URL, remoteDHash)
	remoteAHash := computeHash(remoteAContent)

	remoteBContent := fmt.Sprintf(`
function getRemoteConfigs() {
    return [{ url: "%s/d.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["from-B: hadolint"] };
}`, server.URL, remoteDHash)
	remoteBHash := computeHash(remoteBContent)

	mux.HandleFunc("/a.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteAContent))
	})
	mux.HandleFunc("/b.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteBContent))
	})
	mux.HandleFunc("/d.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteDContent))
	})

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [
        { url: "%s/a.ts", hash: "%s" },
        { url: "%s/b.ts", hash: "%s" }
    ];
}
function getConfig(input) {
    return { ignoreRules: ["from-local: shellcheck"] };
}`, server.URL, remoteAHash, server.URL, remoteBHash)

	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-diamond",
		content: localContent,
	}, resolved, stack)
	if err != nil {
		t.Fatalf("processConfigSource error (diamond should succeed): %v", err)
	}

	// Depth-first: D -> A -> D -> B -> local
	want := []string{"from-D: eslint", "from-A: prettier", "from-D: eslint", "from-B: hadolint", "from-local: shellcheck"}
	if len(result.IgnoreRules) != len(want) {
		t.Fatalf("len(IgnoreRules) = %d, want %d; got %v", len(result.IgnoreRules), len(want), result.IgnoreRules)
	}
	for i, w := range want {
		if result.IgnoreRules[i] != w {
			t.Errorf("IgnoreRules[%d] = %q, want %q", i, result.IgnoreRules[i], w)
		}
	}
}

func TestBeforeConfigOrdering(t *testing.T) {
	beforeDir := t.TempDir()
	beforePath := filepath.Join(beforeDir, "before.js")
	if err := os.WriteFile(beforePath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-before: eslint"] }; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "override.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-override: prettier"] }; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(
		[]string{beforePath},
		true, // skip git root auto-discovery
		[]string{configPath},
	)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	// Order: default -> before-config -> (no auto) -> config
	// Go appends ignoreRules: before rules should come before override rules
	beforeIdx := -1
	overrideIdx := -1
	for i, rule := range cfg.IgnoreRules {
		if rule == "from-before: eslint" {
			beforeIdx = i
		}
		if rule == "from-override: prettier" {
			overrideIdx = i
		}
	}
	if beforeIdx < 0 {
		t.Fatalf("missing 'from-before: eslint' in IgnoreRules: %v", cfg.IgnoreRules)
	}
	if overrideIdx < 0 {
		t.Fatalf("missing 'from-override: prettier' in IgnoreRules: %v", cfg.IgnoreRules)
	}
	if beforeIdx >= overrideIdx {
		t.Errorf("before-config rule (idx=%d) should come before override rule (idx=%d)", beforeIdx, overrideIdx)
	}
}

func TestNoAutoConfig(t *testing.T) {
	cfg, _, _, err := loadConfigWithPaths(nil, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths with noAutoConfig error: %v", err)
	}
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
	if cfg.Apps == nil {
		t.Error("default config should have apps")
	}
}

func TestLoadConfigString(t *testing.T) {
	e, err := engine.New("")
	if err != nil {
		t.Fatalf("engine.New error: %v", err)
	}

	content := `function getConfig(input) { return { ignoreRules: ["test-rule: eslint"] }; }`
	if err := loadConfigString(e, content, "test-source"); err != nil {
		t.Fatalf("loadConfigString error: %v", err)
	}

	getConfigFunc, ok := goja.AssertFunction(e.VM().Get("getConfig"))
	if !ok {
		t.Fatal("getConfig is not a function")
	}

	resultVal, err := getConfigFunc(goja.Undefined(), e.VM().NewObject())
	if err != nil {
		t.Fatalf("getConfig call error: %v", err)
	}

	cfg, err := parseConfigResult(e.VM(), resultVal)
	if err != nil {
		t.Fatalf("parseConfigResult error: %v", err)
	}
	if len(cfg.IgnoreRules) != 1 || cfg.IgnoreRules[0] != "test-rule: eslint" {
		t.Errorf("IgnoreRules = %v, want [test-rule: eslint]", cfg.IgnoreRules)
	}
}

func TestSkipRemoteConfigSkipsHTTPRequests(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		_, _ = w.Write([]byte(`function getConfig(input) { return {}; }`))
	}))
	defer server.Close()

	remoteContent := `function getConfig(input) { return {}; }`
	remoteHash := computeHash(remoteContent)

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/remote.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["local-only: eslint"] };
}`, server.URL, remoteHash)

	// With SkipRemoteConfig=true, no HTTP requests should be made
	oldSkip := SkipRemoteConfig
	SkipRemoteConfig = true
	defer func() { SkipRemoteConfig = oldSkip }()

	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-skip-remote",
		content: localContent,
	}, resolved, stack)
	if err != nil {
		t.Fatalf("processConfigSource error: %v", err)
	}

	if requestCount != 0 {
		t.Errorf("expected 0 HTTP requests with SkipRemoteConfig, got %d", requestCount)
	}

	// Only the local ignoreRules should be present (no remote rules)
	if len(result.IgnoreRules) != 1 {
		t.Fatalf("len(IgnoreRules) = %d, want 1; got %v", len(result.IgnoreRules), result.IgnoreRules)
	}
	if result.IgnoreRules[0] != "local-only: eslint" {
		t.Errorf("IgnoreRules[0] = %q, want %q", result.IgnoreRules[0], "local-only: eslint")
	}

	// No remote URLs should have been recorded in resolved map
	if len(resolved) != 0 {
		t.Errorf("expected empty resolved map with SkipRemoteConfig, got %v", resolved)
	}
}

func TestResolvedRemoteURLsCollected(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	remoteContent := `function getConfig(input) { return { ignoreRules: ["from-remote: eslint"] }; }`
	remoteHash := computeHash(remoteContent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteContent))
	}))
	defer server.Close()

	beforeDir := t.TempDir()
	beforePath := filepath.Join(beforeDir, "with-remote.js")
	beforeContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/remote.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["from-local: prettier"] };
}`, server.URL, remoteHash)
	if err := os.WriteFile(beforePath, []byte(beforeContent), 0644); err != nil {
		t.Fatal(err)
	}

	oldSkip := SkipRemoteConfig
	SkipRemoteConfig = false
	defer func() { SkipRemoteConfig = oldSkip }()

	cfg, _, _, err := loadConfigWithPaths([]string{beforePath}, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	// Check that remote URL was collected
	expectedURL := server.URL + "/remote.ts"
	found := false
	resolvedRemoteURLsMu.Lock()
	urlsCopy := make([]string, len(resolvedRemoteURLs))
	copy(urlsCopy, resolvedRemoteURLs)
	resolvedRemoteURLsMu.Unlock()
	for _, url := range urlsCopy {
		if url == expectedURL {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("resolvedRemoteURLs = %v, expected to contain %q", urlsCopy, expectedURL)
	}

	// Verify both remote and local rules are present
	hasRemote := false
	hasLocal := false
	for _, rule := range cfg.IgnoreRules {
		if rule == "from-remote: eslint" {
			hasRemote = true
		}
		if rule == "from-local: prettier" {
			hasLocal = true
		}
	}
	if !hasRemote {
		t.Errorf("missing remote ignore rule in %v", cfg.IgnoreRules)
	}
	if !hasLocal {
		t.Errorf("missing local ignore rule in %v", cfg.IgnoreRules)
	}
}

// Acceptance tests for Task 7

func TestSharedStorageFlowsThroughConfigChain(t *testing.T) {
	rootDir := t.TempDir()
	rootPath := filepath.Join(rootDir, "root.js")
	if err := os.WriteFile(rootPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
			return { sharedStorage: { "my-key": "root-value", "other": "data" } };
		}`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	childPath := filepath.Join(rootDir, "child.js")
	if err := os.WriteFile(childPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
			var ss = input.sharedStorage || {};
			ss["child-key"] = "child-value";
			return { sharedStorage: ss };
		}`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(
		[]string{rootPath},
		true,
		[]string{childPath},
	)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	if cfg.SharedStorage == nil {
		t.Fatal("SharedStorage is nil")
	}
	if cfg.SharedStorage["my-key"] != "root-value" {
		t.Errorf("SharedStorage[my-key] = %q, want %q", cfg.SharedStorage["my-key"], "root-value")
	}
	if cfg.SharedStorage["other"] != "data" {
		t.Errorf("SharedStorage[other] = %q, want %q", cfg.SharedStorage["other"], "data")
	}
	if cfg.SharedStorage["child-key"] != "child-value" {
		t.Errorf("SharedStorage[child-key] = %q, want %q", cfg.SharedStorage["child-key"], "child-value")
	}
}

func TestSharedStorageEmptyByDefault(t *testing.T) {
	cfg, _, _, err := loadConfigWithPaths(nil, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}
	// Default config now includes datamitsu-agent-prompt in SharedStorage
	if len(cfg.SharedStorage) != 1 {
		t.Errorf("SharedStorage should have 1 entry by default, got %d: %v", len(cfg.SharedStorage), cfg.SharedStorage)
	}
	if _, ok := cfg.SharedStorage["datamitsu-agent-prompt"]; !ok {
		t.Errorf("SharedStorage should contain datamitsu-agent-prompt by default")
	}
}

func TestAcceptanceRemoteConfigCachesOnDisk(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cacheDir)

	remoteContent := `function getConfig(input) { return { ignoreRules: ["remote-cached: eslint"] }; }`
	remoteHash := computeHash(remoteContent)

	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		_, _ = w.Write([]byte(remoteContent))
	}))
	defer server.Close()

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/remote.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["local: prettier"] };
}`, server.URL, remoteHash)

	oldSkip := SkipRemoteConfig
	SkipRemoteConfig = false
	defer func() { SkipRemoteConfig = oldSkip }()

	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-cache-on-disk",
		content: localContent,
	}, resolved, stack)
	if err != nil {
		t.Fatalf("processConfigSource error: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("expected 1 HTTP request, got %d", requestCount)
	}

	// Verify remote rules were applied
	hasRemote := false
	for _, r := range result.IgnoreRules {
		if r == "remote-cached: eslint" {
			hasRemote = true
		}
	}
	if !hasRemote {
		t.Errorf("missing remote rule in %v", result.IgnoreRules)
	}

	// Verify cache file exists on disk (remote configs stored under store path)
	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "store", ".remote-configs", "*.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cacheFiles) != 1 {
		t.Errorf("expected 1 cached remote config file, found %d", len(cacheFiles))
	}
}

func TestAcceptanceRepeatedRunUsesCache(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cacheDir)

	remoteContent := `function getConfig(input) { return { ignoreRules: ["from-remote: eslint"] }; }`
	remoteHash := computeHash(remoteContent)

	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		_, _ = w.Write([]byte(remoteContent))
	}))
	defer server.Close()

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/remote.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["from-local: prettier"] };
}`, server.URL, remoteHash)

	oldSkip := SkipRemoteConfig
	SkipRemoteConfig = false
	defer func() { SkipRemoteConfig = oldSkip }()

	// First call — should fetch from server
	resolved1 := make(map[string]bool)
	stack1 := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-repeat-1",
		content: localContent,
	}, resolved1, stack1)
	if err != nil {
		t.Fatalf("first processConfigSource error: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("first call: expected 1 HTTP request, got %d", requestCount)
	}

	// Second call — should use cache, no additional HTTP request
	resolved2 := make(map[string]bool)
	stack2 := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-repeat-2",
		content: localContent,
	}, resolved2, stack2)
	if err != nil {
		t.Fatalf("second processConfigSource error: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("second call: expected still 1 HTTP request (cached), got %d", requestCount)
	}

	// Verify result is correct from cache
	hasRemote := false
	for _, r := range result.IgnoreRules {
		if r == "from-remote: eslint" {
			hasRemote = true
		}
	}
	if !hasRemote {
		t.Errorf("missing remote rule from cache in %v", result.IgnoreRules)
	}
}

func TestAcceptanceCacheHitWhenServerDown(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cacheDir)

	remoteContent := `function getConfig(input) { return { ignoreRules: ["cached-remote: eslint"] }; }`
	remoteHash := computeHash(remoteContent)

	serverDown := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serverDown {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(remoteContent))
	}))
	defer server.Close()

	localContent := fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() {
    return [{ url: "%s/remote.ts", hash: "%s" }];
}
function getConfig(input) {
    return { ignoreRules: ["local: prettier"] };
}`, server.URL, remoteHash)

	oldSkip := SkipRemoteConfig
	SkipRemoteConfig = false
	defer func() { SkipRemoteConfig = oldSkip }()

	// First call — populate cache
	resolved1 := make(map[string]bool)
	stack1 := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-cache-1",
		content: localContent,
	}, resolved1, stack1)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Server goes down — cached content hash still matches, so cache hit
	serverDown = true

	resolved2 := make(map[string]bool)
	stack2 := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-cache-2",
		content: localContent,
	}, resolved2, stack2)
	if err != nil {
		t.Fatalf("expected cache hit, got error: %v", err)
	}

	hasRemote := false
	for _, r := range result.IgnoreRules {
		if r == "cached-remote: eslint" {
			hasRemote = true
		}
	}
	if !hasRemote {
		t.Errorf("missing cached remote rule in result: %v", result.IgnoreRules)
	}
}

// Tests for getMinVersion() extraction from JS config (Task 3 TDD)

func TestProcessConfigSourceWithGetMinVersion(t *testing.T) {
	// Config that exports getMinVersion() returning a valid semver string.
	// With ldflags.Version="dev" (normalized to v0.0.0), requiring "0.0.1"
	// would fail, so we require "0.0.0" to test the success path.
	content := `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["with-version: eslint"] }; }
`
	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	result, _, err := processConfigSource(nil, configSource{
		name:    "test-with-min-version",
		content: content,
	}, resolved, stack)
	if err != nil {
		t.Fatalf("processConfigSource should succeed with valid getMinVersion, got error: %v", err)
	}

	hasRule := false
	for _, r := range result.IgnoreRules {
		if r == "with-version: eslint" {
			hasRule = true
		}
	}
	if !hasRule {
		t.Errorf("expected ignore rule from config, got %v", result.IgnoreRules)
	}
}

func TestProcessConfigSourceWithoutGetMinVersion(t *testing.T) {
	// Config that does NOT export getMinVersion() should produce an error.
	content := `
function getConfig(input) { return { ignoreRules: ["no-version: eslint"] }; }
`
	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-no-min-version",
		content: content,
	}, resolved, stack)
	if err == nil {
		t.Fatal("expected error when getMinVersion is not exported")
	}
	if !strings.Contains(err.Error(), "getMinVersion") {
		t.Errorf("error should mention getMinVersion, got: %v", err)
	}
}

func TestProcessConfigSourceGetMinVersionReturnsNonString(t *testing.T) {
	// getMinVersion() returns a number instead of a string — should error.
	content := `
function getMinVersion() { return 42; }
function getConfig(input) { return {}; }
`
	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-non-string-version",
		content: content,
	}, resolved, stack)
	if err == nil {
		t.Fatal("expected error when getMinVersion returns non-string value")
	}
	if !strings.Contains(err.Error(), "getMinVersion") {
		t.Errorf("error should mention getMinVersion, got: %v", err)
	}
}

func TestProcessConfigSourceGetMinVersionReturnsEmpty(t *testing.T) {
	// getMinVersion() returns an empty string — should error.
	content := `
function getMinVersion() { return ""; }
function getConfig(input) { return {}; }
`
	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-empty-version",
		content: content,
	}, resolved, stack)
	if err == nil {
		t.Fatal("expected error when getMinVersion returns empty string")
	}
	if !strings.Contains(err.Error(), "getMinVersion") {
		t.Errorf("error should mention getMinVersion, got: %v", err)
	}
}

func TestProcessConfigSourceGetMinVersionReturnsInvalidSemver(t *testing.T) {
	// getMinVersion() returns an invalid semver string — should error during comparison.
	content := `
function getMinVersion() { return "not-a-version"; }
function getConfig(input) { return {}; }
`
	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	_, _, err := processConfigSource(nil, configSource{
		name:    "test-invalid-semver",
		content: content,
	}, resolved, stack)
	if err == nil {
		t.Fatal("expected error when getMinVersion returns invalid semver")
	}
	if !strings.Contains(err.Error(), "invalid") || !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention invalid version, got: %v", err)
	}
}

// Tests for version validation in config loading pipeline (Task 5 TDD)

func TestLoadConfigWithLowMinVersion(t *testing.T) {
	// Config with getMinVersion="0.0.1" should succeed when current version is "dev" (v0.0.0).
	// Wait — "dev" normalizes to v0.0.0, which is LESS than v0.0.1. So we use "0.0.0".
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "low-version.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["low-version: eslint"] }; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(nil, true, []string{configPath})
	if err != nil {
		t.Fatalf("expected success with low minVersion, got error: %v", err)
	}

	hasRule := false
	for _, r := range cfg.IgnoreRules {
		if r == "low-version: eslint" {
			hasRule = true
		}
	}
	if !hasRule {
		t.Errorf("expected ignore rule from config, got %v", cfg.IgnoreRules)
	}
}

func TestLoadConfigWithHighMinVersionFails(t *testing.T) {
	// Config with getMinVersion="99.0.0" should fail because current version
	// ("dev" -> v0.0.0) is less than required.
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "high-version.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return {}; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := loadConfigWithPaths(nil, true, []string{configPath})
	if err == nil {
		t.Fatal("expected error when minVersion > current version")
	}
	if !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("error should contain upgrade instructions, got: %v", err)
	}
	if !strings.Contains(err.Error(), "v99.0.0") {
		t.Errorf("error should mention required version v99.0.0, got: %v", err)
	}
}

func TestLoadConfigWithDevVersionAlwaysPasses(t *testing.T) {
	// When getMinVersion returns "dev", it normalizes to v0.0.0 which is
	// always <= current version (also "dev" -> v0.0.0). This should pass.
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "dev-version.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["dev-version: eslint"] }; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	// ldflags.Version defaults to "dev" which normalizes to v0.0.0
	cfg, _, _, err := loadConfigWithPaths(nil, true, []string{configPath})
	if err != nil {
		t.Fatalf("expected success with dev version, got error: %v", err)
	}
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
}

func TestLoadConfigMultiLayerVersionCheck(t *testing.T) {
	// Test that version checking works across multiple config layers:
	// before-config + explicit config. Each layer should be checked independently.
	beforeDir := t.TempDir()
	beforePath := filepath.Join(beforeDir, "before.js")
	if err := os.WriteFile(beforePath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-before: eslint"] }; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-config: prettier"] }; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths([]string{beforePath}, true, []string{configPath})
	if err != nil {
		t.Fatalf("expected success with multi-layer version check, got error: %v", err)
	}

	hasBefore := false
	hasConfig := false
	for _, r := range cfg.IgnoreRules {
		if r == "from-before: eslint" {
			hasBefore = true
		}
		if r == "from-config: prettier" {
			hasConfig = true
		}
	}
	if !hasBefore {
		t.Errorf("missing before-config rule in %v", cfg.IgnoreRules)
	}
	if !hasConfig {
		t.Errorf("missing config rule in %v", cfg.IgnoreRules)
	}
}

func TestLoadConfigMultiLayerVersionCheckFailsOnSecondLayer(t *testing.T) {
	// First config layer passes, second layer has high version requirement -> fails.
	// Error message should identify which config file failed.
	beforeDir := t.TempDir()
	beforePath := filepath.Join(beforeDir, "before.js")
	if err := os.WriteFile(beforePath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-before: eslint"] }; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "failing-config.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return {}; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := loadConfigWithPaths([]string{beforePath}, true, []string{configPath})
	if err == nil {
		t.Fatal("expected error when second layer has high version requirement")
	}
	// Error should reference the failing config path
	if !strings.Contains(err.Error(), configPath) {
		t.Errorf("error should mention the failing config path %q, got: %v", configPath, err)
	}
	if !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("error should contain upgrade instructions, got: %v", err)
	}
}

func TestLoadConfigVersionCheckShowsConfigFile(t *testing.T) {
	// Verify that version check failure error message includes the config file name.
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "my-special-config.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return {}; }`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := loadConfigWithPaths(nil, true, []string{configPath})
	if err == nil {
		t.Fatal("expected error for version check failure")
	}
	// The error should include the config file path so users know which config needs updating
	if !strings.Contains(err.Error(), configPath) {
		t.Errorf("error should contain config path %q, got: %v", configPath, err)
	}
}

func TestDefaultConfigHasGetMinVersion(t *testing.T) {
	// The embedded default config (config.js) should export getMinVersion().
	// Even though the default config is skipped for version checking (isDefault=true),
	// it should still define the function so user configs that override it inherit
	// a consistent contract.
	e, err := engine.New("")
	if err != nil {
		t.Fatalf("engine.New error: %v", err)
	}

	defaultJS, err := config.GetDefaultConfig()
	if err != nil {
		t.Fatalf("GetDefaultConfig error: %v", err)
	}
	if err := loadConfigString(e, defaultJS, "default-config"); err != nil {
		t.Fatalf("loadConfigString error: %v", err)
	}

	vm := e.VM()
	getMinVersionVal := vm.Get("getMinVersion")
	if getMinVersionVal == nil || goja.IsUndefined(getMinVersionVal) || goja.IsNull(getMinVersionVal) {
		t.Fatal("default config does not export getMinVersion")
	}

	fn, ok := goja.AssertFunction(getMinVersionVal)
	if !ok {
		t.Fatal("getMinVersion is not a function in default config")
	}

	result, err := fn(goja.Undefined())
	if err != nil {
		t.Fatalf("getMinVersion() call failed: %v", err)
	}

	version, ok := result.Export().(string)
	if !ok {
		t.Fatalf("getMinVersion() returned non-string: %T", result.Export())
	}
	if version == "" {
		t.Fatal("getMinVersion() returned empty string")
	}
	if version != "0.0.1" {
		t.Errorf("getMinVersion() = %q, want %q", version, "0.0.1")
	}
}

func TestProcessConfigSourceGetMinVersionExtractionFromVM(t *testing.T) {
	// Verify that getMinVersion() is correctly extracted and its value
	// is used for version validation. A config with v-prefixed version
	// should work the same as without prefix.
	tests := []struct {
		name    string
		version string
	}{
		{"bare semver", "0.0.0"},
		{"v-prefixed", "v0.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := fmt.Sprintf(`
function getMinVersion() { return "%s"; }
function getConfig(input) { return {}; }
`, tt.version)
			resolved := make(map[string]bool)
			stack := make(map[string]bool)
			_, _, err := processConfigSource(nil, configSource{
				name:    "test-extraction-" + tt.name,
				content: content,
			}, resolved, stack)
			if err != nil {
				t.Fatalf("expected success for version %q, got error: %v", tt.version, err)
			}
		})
	}
}

// ========================================
// Eager content evaluation (layerMap) tests
// ========================================

func TestLoadConfigImplInitializesLayerMap(t *testing.T) {
	_, layerMap, _, err := loadConfigWithPaths(nil, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}
	if layerMap == nil {
		t.Fatal("layerMap should not be nil")
	}
}

func TestLoadConfigImplReturns4Tuple(t *testing.T) {
	cfg, layerMap, vm, err := loadConfigWithPaths(nil, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}
	if cfg == nil {
		t.Error("cfg should not be nil")
	}
	if layerMap == nil {
		t.Error("layerMap should not be nil")
	}
	if vm == nil {
		t.Error("vm should not be nil")
	}
}

func TestLoadConfigImplEvaluatesInitContent(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "test-init.js")
	if err := os.WriteFile(configPath, []byte(`
function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
    return {
        init: {
            ".editorconfig": {
                scope: "git-root",
                content: function(context) { return "root = true"; }
            }
        }
    };
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, layerMap, _, err := loadConfigWithPaths(nil, true, []string{configPath})
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	if layerMap == nil {
		t.Fatal("layerMap should not be nil")
	}

	history, ok := (*layerMap)[".editorconfig"]
	if !ok {
		t.Fatal("expected .editorconfig in layerMap")
	}

	lastContent := config.GetLastGeneratedContent(history)
	if lastContent == nil {
		t.Fatal("expected generated content for .editorconfig")
	}
	if *lastContent != "root = true" {
		t.Errorf("expected 'root = true', got %q", *lastContent)
	}
}

func TestLoadConfigImplMergesLayersAcrossSources(t *testing.T) {
	beforeDir := t.TempDir()
	beforePath := filepath.Join(beforeDir, "before.js")
	if err := os.WriteFile(beforePath, []byte(`
function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
    return {
        init: {
            ".editorconfig": {
                scope: "git-root",
                content: function(context) { return "from-before"; }
            }
        }
    };
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "override.js")
	if err := os.WriteFile(configPath, []byte(`
function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
    return {
        init: {
            ".editorconfig": {
                scope: "git-root",
                content: function(context) {
                    if (context.existingContent) {
                        return context.existingContent + "\nindent_size = 2";
                    }
                    return "fallback";
                }
            }
        }
    };
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, layerMap, _, err := loadConfigWithPaths([]string{beforePath}, true, []string{configPath})
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	history := (*layerMap)[".editorconfig"]
	if history == nil {
		t.Fatal("expected .editorconfig in layerMap")
	}

	lastContent := config.GetLastGeneratedContent(history)
	if lastContent == nil {
		t.Fatal("expected generated content")
	}
	if *lastContent != "from-before\nindent_size = 2" {
		t.Errorf("expected 'from-before\\nindent_size = 2', got %q", *lastContent)
	}
}

func TestLoadConfigImplSkipsFailedContentEvaluation(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "bad-content.js")
	if err := os.WriteFile(configPath, []byte(`
function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
    return {
        init: {
            "test-fail-eval.txt": {
                scope: "git-root",
                content: function(context) { throw new Error("content generation failed"); }
            }
        }
    };
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, layerMap, _, err := loadConfigWithPaths(nil, true, []string{configPath})
	if err != nil {
		t.Fatalf("loadConfigWithPaths should not error on failed content(): %v", err)
	}

	history := (*layerMap)["test-fail-eval.txt"]
	if history == nil {
		t.Fatal("expected test-fail-eval.txt in layerMap even when content() fails")
	}
	lastContent := config.GetLastGeneratedContent(history)
	if lastContent != nil {
		t.Errorf("expected nil generated content for failed content(), got %q", *lastContent)
	}
}
