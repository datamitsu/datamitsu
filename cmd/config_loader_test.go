package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/engine"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"

	"github.com/dop251/goja"
	"github.com/goccy/go-yaml"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// swapLoggerWithObserver replaces logger.Logger with a zap observer for the
// duration of the test so warnings emitted by the config loader can be
// asserted. The observer captures entries at the given level and above.
func swapLoggerWithObserver(t *testing.T, level zapcore.LevelEnabler) *observer.ObservedLogs {
	t.Helper()
	core, observed := observer.New(level)
	original := logger.Logger
	logger.Logger = zap.New(core)
	t.Cleanup(func() { logger.Logger = original })
	return observed
}

func TestLoadConfig(t *testing.T) {
	// Load only the embedded default config (noAutoConfig) so the assertions are
	// hermetic — independent of any datamitsu.config.* at this repo's git root.
	cfg, _, vm, err := loadConfigWithPaths(context.Background(), nil, true, nil)
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
	// Hermetic: assert the embedded default config, ignoring any git-root config.
	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, nil)
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

	nodeRuntime, ok := cfg.Runtimes["node"]
	if !ok {
		t.Fatal("node runtime not found in config")
	}
	if nodeRuntime.Kind != config.RuntimeKindNode {
		t.Errorf("node runtime kind = %q, want %q", nodeRuntime.Kind, config.RuntimeKindNode)
	}
	if nodeRuntime.Mode != config.RuntimeModeManaged {
		t.Errorf("node runtime mode = %q, want %q", nodeRuntime.Mode, config.RuntimeModeManaged)
	}
	if nodeRuntime.Node == nil {
		t.Fatal("node runtime node config is nil")
	}
	if nodeRuntime.Node.NodeVersion == "" {
		t.Error("node runtime nodeVersion is empty")
	}
	if nodeRuntime.Node.PNPMVersion == "" {
		t.Error("node runtime pnpmVersion is empty")
	}
	if nodeRuntime.Node.PNPMHash == "" {
		t.Error("node runtime pnpmHash is empty")
	}
	if nodeRuntime.Managed == nil {
		t.Fatal("node runtime managed config is nil")
	}
	// Node is acquired as a direct archive (jvm-style): linux must carry both a
	// glibc and a musl entry, plus a darwin entry, all extracted to a directory.
	linuxNode, ok := nodeRuntime.Managed.Binaries["linux"]
	if !ok {
		t.Fatal("node runtime missing linux binaries")
	}
	amd64Node, ok := linuxNode["amd64"]
	if !ok {
		t.Fatal("node runtime missing linux/amd64 binaries")
	}
	glibcNode, ok := amd64Node["glibc"]
	if !ok {
		t.Fatal("node runtime missing linux/amd64/glibc entry")
	}
	if !glibcNode.ExtractDir {
		t.Error("node runtime linux/amd64/glibc should set extractDir")
	}
	if glibcNode.Hash == "" {
		t.Error("node runtime linux/amd64/glibc hash is empty (hash pinning is mandatory)")
	}
	if _, ok := amd64Node["musl"]; !ok {
		t.Error("node runtime missing linux/amd64/musl entry (static musl archive)")
	}
	if _, ok := nodeRuntime.Managed.Binaries["darwin"]; !ok {
		t.Error("node runtime missing darwin binaries")
	}
}

func TestLoadConfigRuntimeApps(t *testing.T) {
	// Hermetic: assert the embedded default config's demo apps, ignoring any
	// datamitsu.config.* at this repo's git root (which overlays its own apps).
	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, nil)
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

	// Test Node app
	jscowsay, ok := cfg.Apps["jscowsay"]
	if !ok {
		t.Fatal("jscowsay app not found")
	}
	if jscowsay.Node == nil {
		t.Fatal("jscowsay.Node is nil")
	}
	if jscowsay.Node.PackageName != "cowsay" {
		t.Errorf("jscowsay packageName = %q, want %q", jscowsay.Node.PackageName, "cowsay")
	}
	if jscowsay.Node.BinPath == "" {
		t.Error("jscowsay.Node.BinPath is empty")
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
			setup: {
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

	claudeInit, ok := cfg.Setup["CLAUDE.md"]
	if !ok {
		t.Fatal("CLAUDE.md init config not found")
	}
	if claudeInit.LinkTarget != "AGENTS.md" {
		t.Errorf("CLAUDE.md LinkTarget = %q, want %q", claudeInit.LinkTarget, "AGENTS.md")
	}
	if claudeInit.Scope != "git-root" {
		t.Errorf("CLAUDE.md Scope = %q, want %q", claudeInit.Scope, "git-root")
	}

	cursorInit, ok := cfg.Setup[".cursorrules"]
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
			setup: {
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

	cursorInit, ok := cfg.Setup[".cursor/rules"]
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
			setup: {
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

	gitignoreInit, ok := cfg.Setup[".gitignore"]
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
			setup: {
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

	claudeInit := cfg.Setup["CLAUDE.md"]
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
			setup: {
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

	testInit := cfg.Setup["test.json"]
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
	if err := os.WriteFile(tsPath, []byte("// ts config"), 0o644); err != nil {
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
	if err := os.WriteFile(jsPath, []byte("// js config"), 0o644); err != nil {
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
	if err := os.WriteFile(mjsPath, []byte("// mjs config"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, ldflags.PackageName+".config.ts"), []byte("// ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ldflags.PackageName+".config.js"), []byte("// js"), 0o644); err != nil {
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

// writeBeforeConfigAuto writes an auto-discovery config file at dir's git-root
// path whose getBeforeConfigs() body is the given JS array literal, and returns
// the config path. Pass an empty body to omit getBeforeConfigs entirely.
func writeBeforeConfigAuto(t *testing.T, dir, getBeforeConfigsBody string) string {
	t.Helper()
	autoPath := filepath.Join(dir, ldflags.PackageName+".config.js")
	var src string
	if getBeforeConfigsBody == "" {
		src = `function getConfig(input) { return {}; }`
	} else {
		src = fmt.Sprintf(
			"function getBeforeConfigs() { return %s; }\nfunction getConfig(input) { return {}; }",
			getBeforeConfigsBody,
		)
	}
	if err := os.WriteFile(autoPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return autoPath
}

func writeStubConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`function getConfig(input) { return {}; }`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverBeforeConfigsAbsent(t *testing.T) {
	dir := t.TempDir()
	autoPath := writeBeforeConfigAuto(t, dir, "")

	got, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil when getBeforeConfigs is absent", got)
	}
}

func TestDiscoverBeforeConfigsEmptyArray(t *testing.T) {
	dir := t.TempDir()
	autoPath := writeBeforeConfigAuto(t, dir, "[]")

	got, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty for []", got)
	}
}

func TestDiscoverBeforeConfigsRelativePath(t *testing.T) {
	dir := t.TempDir()
	sharedPath := filepath.Join(dir, "shared.js")
	writeStubConfig(t, sharedPath)
	autoPath := writeBeforeConfigAuto(t, dir, `[{ path: "./shared.js" }]`)

	got, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{sharedPath}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiscoverBeforeConfigsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	targetDir := t.TempDir()
	sharedPath := filepath.Join(targetDir, "shared.js")
	writeStubConfig(t, sharedPath)
	autoPath := writeBeforeConfigAuto(t, dir, fmt.Sprintf(`[{ path: %q }]`, sharedPath))

	got, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{sharedPath}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiscoverBeforeConfigsPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.js", "b.js", "c.js"} {
		writeStubConfig(t, filepath.Join(dir, name))
	}
	autoPath := writeBeforeConfigAuto(t, dir, `[{ path: "./c.js" }, { path: "./a.js" }, { path: "./b.js" }]`)

	got, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		filepath.Join(dir, "c.js"),
		filepath.Join(dir, "a.js"),
		filepath.Join(dir, "b.js"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiscoverBeforeConfigsEmptyPathErrors(t *testing.T) {
	dir := t.TempDir()
	autoPath := writeBeforeConfigAuto(t, dir, `[{ path: "" }]`)

	_, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("error = %q, want it to contain 'path is required'", err.Error())
	}
}

func TestDiscoverBeforeConfigsNonExistentFileErrors(t *testing.T) {
	dir := t.TempDir()
	autoPath := writeBeforeConfigAuto(t, dir, `[{ path: "./does-not-exist.js" }]`)

	_, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err == nil {
		t.Fatal("expected error for non-existent before config file")
	}
	if !strings.Contains(err.Error(), "does-not-exist.js") {
		t.Errorf("error = %q, want it to mention the missing file", err.Error())
	}
}

func TestDiscoverBeforeConfigsDedup(t *testing.T) {
	dir := t.TempDir()
	sharedPath := filepath.Join(dir, "shared.js")
	writeStubConfig(t, sharedPath)
	// Same file declared twice: once relative, once absolute — both resolve to
	// the same cleaned path and dedup to a single entry.
	autoPath := writeBeforeConfigAuto(t, dir, fmt.Sprintf(`[{ path: "./shared.js" }, { path: %q }]`, sharedPath))

	got, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d paths, want 1 (deduped): %v", len(got), got)
	}
	if got[0] != sharedPath {
		t.Errorf("got %q, want %q", got[0], sharedPath)
	}
}

func TestDiscoverBeforeConfigsNonArrayReturnErrors(t *testing.T) {
	dir := t.TempDir()
	// getBeforeConfigs() returning a non-array value cannot be exported into
	// []beforeConfigEntry — a config author typo must surface a clear error.
	autoPath := writeBeforeConfigAuto(t, dir, `"not-an-array"`)

	_, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err == nil {
		t.Fatal("expected error for non-array getBeforeConfigs return")
	}
	if !strings.Contains(err.Error(), "failed to parse getBeforeConfigs") {
		t.Errorf("error = %q, want it to contain 'failed to parse getBeforeConfigs'", err.Error())
	}
}

func TestDiscoverBeforeConfigsFunctionThrowsErrors(t *testing.T) {
	dir := t.TempDir()
	autoPath := filepath.Join(dir, ldflags.PackageName+".config.js")
	src := "function getBeforeConfigs() { throw new Error('boom'); }\n" +
		"function getConfig(input) { return {}; }"
	if err := os.WriteFile(autoPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := discoverBeforeConfigs(context.Background(), autoPath)
	if err == nil {
		t.Fatal("expected error when getBeforeConfigs throws")
	}
	if !strings.Contains(err.Error(), "failed to call getBeforeConfigs") {
		t.Errorf("error = %q, want it to contain 'failed to call getBeforeConfigs'", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to propagate the thrown message 'boom'", err.Error())
	}
}

func TestBuildConfigSourcesPropagatesDiscoverError(t *testing.T) {
	dir := t.TempDir()
	// A malformed getBeforeConfigs() in the auto config must fail the whole
	// source assembly, not be silently swallowed.
	autoPath := writeBeforeConfigAuto(t, dir, `"not-an-array"`)

	_, err := buildConfigSources(context.Background(), nil, autoPath, nil)
	if err == nil {
		t.Fatal("expected buildConfigSources to propagate the discoverBeforeConfigs error")
	}
	if !strings.Contains(err.Error(), "failed to parse getBeforeConfigs") {
		t.Errorf("error = %q, want it to contain 'failed to parse getBeforeConfigs'", err.Error())
	}
}

func sourceNames(sources []configSource) []string {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.name
	}
	return names
}

func TestBuildConfigSourcesDefaultOnly(t *testing.T) {
	sources, err := buildConfigSources(context.Background(), nil, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1: %v", len(sources), sourceNames(sources))
	}
	if !sources[0].isDefault || sources[0].name != "default" {
		t.Errorf("source[0] = %+v, want the default source", sources[0])
	}
}

func TestBuildConfigSourcesBeforeConfigFlag(t *testing.T) {
	sources, err := buildConfigSources(context.Background(), []string{"/before/a.js", "/before/b.js"}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"default", "/before/a.js", "/before/b.js"}
	if got := sourceNames(sources); !slices.Equal(got, want) {
		t.Fatalf("source names = %v, want %v", got, want)
	}
	// Flag paths carry their path through, unlike the embedded default.
	if sources[1].path != "/before/a.js" {
		t.Errorf("source[1].path = %q, want %q", sources[1].path, "/before/a.js")
	}
}

func TestBuildConfigSourcesConfigPathsAfterAuto(t *testing.T) {
	dir := t.TempDir()
	autoPath := writeBeforeConfigAuto(t, dir, "")

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "explicit.js")
	writeStubConfig(t, cfgPath)

	sources, err := buildConfigSources(context.Background(), nil, autoPath, []string{cfgPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// default -> auto -> explicit (no declared before-configs in this auto config)
	want := []string{"default", "auto", cfgPath}
	if got := sourceNames(sources); !slices.Equal(got, want) {
		t.Fatalf("source names = %v, want %v", got, want)
	}
}

func TestBuildConfigSourcesDeclaredBeforeConfigs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"first.js", "second.js"} {
		writeStubConfig(t, filepath.Join(dir, name))
	}
	autoPath := writeBeforeConfigAuto(t, dir, `[{ path: "./first.js" }, { path: "./second.js" }]`)

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "explicit.js")
	writeStubConfig(t, cfgPath)

	sources, err := buildConfigSources(context.Background(), nil, autoPath, []string{cfgPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// default -> declared(first, second) -> auto -> explicit
	want := []string{
		"default",
		filepath.Join(dir, "first.js"),
		filepath.Join(dir, "second.js"),
		"auto",
		cfgPath,
	}
	if got := sourceNames(sources); !slices.Equal(got, want) {
		t.Fatalf("source names = %v, want %v", got, want)
	}
	if sources[3].path != autoPath {
		t.Errorf("auto source path = %q, want %q", sources[3].path, autoPath)
	}
}

func TestBuildConfigSourcesFlagSkipsDeclared(t *testing.T) {
	dir := t.TempDir()
	writeStubConfig(t, filepath.Join(dir, "declared.js"))
	autoPath := writeBeforeConfigAuto(t, dir, `[{ path: "./declared.js" }]`)

	flagDir := t.TempDir()
	flagPath := filepath.Join(flagDir, "flag.js")
	writeStubConfig(t, flagPath)

	sources, err := buildConfigSources(context.Background(), []string{flagPath}, autoPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// default -> flag -> auto; declared.js must be absent (flag wins).
	want := []string{"default", flagPath, "auto"}
	if got := sourceNames(sources); !slices.Equal(got, want) {
		t.Fatalf("source names = %v, want %v", got, want)
	}
	declaredPath := filepath.Join(dir, "declared.js")
	for _, s := range sources {
		if s.name == declaredPath || s.path == declaredPath {
			t.Errorf("declared before-config %q should be absent when --before-config flag is set", declaredPath)
		}
	}
}

func computeHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(h[:])
}

// ===========================================================================
// Integration tests: getBeforeConfigs() end-to-end parity, root-only scope,
// and --before-config flag precedence (Task 3).
//
// These exercise the real auto-discovery path through loadConfigWithPaths,
// which resolves the git root from the working directory. Each test therefore
// runs in a freshly git-initialized temp dir and changes into it, so they must
// not run in parallel.
// ===========================================================================

func gitAvailable() bool {
	return exec.Command("git", "--version").Run() == nil
}

// setupGitRoot creates a git-initialized temp dir, changes into it, and returns
// its symlink-resolved path. The resolution matters because
// `git rev-parse --show-toplevel` (used by facts.GetGitRoot) returns the
// canonical path; writing config files under the resolved path keeps them
// discoverable as the auto config.
func setupGitRoot(t *testing.T) string {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = resolved
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	t.Chdir(resolved)
	return resolved
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jvmApp renders a valid jvm app literal (jarHash is a 64-char lowercase hex
// string so it passes config validation; jvm apps need no lockfile).
func jvmApp(hashChar string, version string) string {
	return fmt.Sprintf(
		`{ jvm: { jarUrl: "https://example.com/x.jar", jarHash: %q, version: %q } }`,
		strings.Repeat(hashChar, 64), version,
	)
}

// effectiveSnapshot captures the observable, source-name-independent result of
// a config load so two runs can be compared for parity.
type effectiveSnapshot struct {
	ignoreRules  []string
	appKeys      []string
	jvmVersions  map[string]string
	editorconfig string
}

func snapshotEffective(t *testing.T, cfg *config.Config, layerMap *config.SetupLayerMap) effectiveSnapshot {
	t.Helper()
	snap := effectiveSnapshot{
		ignoreRules: cfg.IgnoreRules,
		jvmVersions: make(map[string]string),
	}
	for name, app := range cfg.Apps {
		snap.appKeys = append(snap.appKeys, name)
		if app.Jvm != nil {
			snap.jvmVersions[name] = app.Jvm.Version
		}
	}
	slices.Sort(snap.appKeys)
	if layerMap != nil {
		if history, ok := (*layerMap)[".editorconfig"]; ok {
			if c := config.GetLastGeneratedContent(history); c != nil {
				snap.editorconfig = *c
			}
		}
	}
	return snap
}

// TestBeforeConfigsDeclaredParityWithFlag asserts that an auto config which
// declares a before-config via getBeforeConfigs() produces the same effective
// config (apps overridable by the auto layer, init layered identically,
// ignoreRules ordered identically) as the equivalent explicit invocation that
// passes the same shared file via --before-config and the auto file via
// --config. Both resolve to: default -> shared -> auto.
func TestBeforeConfigsDeclaredParityWithFlag(t *testing.T) {
	root := setupGitRoot(t)

	mergeHelper := `function _merge(base, extra) { var out = {}; var k; for (k in base) { out[k] = base[k]; } for (k in extra) { out[k] = extra[k]; } return out; }`

	sharedPath := filepath.Join(root, "shared.js")
	writeFile(t, sharedPath, fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
%s
function getConfig(input) {
    var apps = _merge(input && input.apps ? input.apps : {}, { "shared-tool": %s });
    return {
        apps: apps,
        ignoreRules: ["from-shared: eslint"],
        setup: { ".editorconfig": { scope: "git-root", content: function(ctx) { return "from-shared"; } } }
    };
}`, mergeHelper, jvmApp("a", "1.0.0")))

	autoPath := filepath.Join(root, ldflags.PackageName+".config.js")
	writeFile(t, autoPath, fmt.Sprintf(`
function getBeforeConfigs() { return [{ path: "./shared.js" }]; }
function getMinVersion() { return "0.0.0"; }
%s
function getConfig(input) {
    var apps = _merge(input && input.apps ? input.apps : {}, {
        "shared-tool": %s,
        "auto-tool": %s
    });
    return {
        apps: apps,
        ignoreRules: ["from-auto: prettier"],
        setup: { ".editorconfig": { scope: "git-root", content: function(ctx) { return (ctx.existingContent || "") + "\nfrom-auto"; } } }
    };
}`, mergeHelper, jvmApp("a", "2.0.0"), jvmApp("b", "1.0.0")))

	// Run A: declared before-config via auto discovery.
	cfgA, lmA, _, errA := loadConfigWithPaths(context.Background(), nil, false, nil)
	if errA != nil {
		t.Fatalf("declared run error: %v", errA)
	}
	// Run B: explicit --before-config (shared) + --config (auto), auto-discovery off.
	cfgB, lmB, _, errB := loadConfigWithPaths(context.Background(), []string{sharedPath}, true, []string{autoPath})
	if errB != nil {
		t.Fatalf("flag run error: %v", errB)
	}

	snapA := snapshotEffective(t, cfgA, lmA)
	snapB := snapshotEffective(t, cfgB, lmB)

	if !slices.Equal(snapA.ignoreRules, snapB.ignoreRules) {
		t.Errorf("ignoreRules differ:\n declared=%v\n flag=%v", snapA.ignoreRules, snapB.ignoreRules)
	}
	if !slices.Equal(snapA.appKeys, snapB.appKeys) {
		t.Errorf("app keys differ:\n declared=%v\n flag=%v", snapA.appKeys, snapB.appKeys)
	}
	if !maps.Equal(snapA.jvmVersions, snapB.jvmVersions) {
		t.Errorf("jvm versions differ:\n declared=%v\n flag=%v", snapA.jvmVersions, snapB.jvmVersions)
	}
	if snapA.editorconfig != snapB.editorconfig {
		t.Errorf("init content differs:\n declared=%q\n flag=%q", snapA.editorconfig, snapB.editorconfig)
	}

	// Sanity-anchor the parity (so equality is not trivially true): the auto
	// layer overrode shared-tool's version, added auto-tool, layered init, and
	// shared's ignoreRule precedes auto's.
	if got := snapA.jvmVersions["shared-tool"]; got != "2.0.0" {
		t.Errorf("shared-tool version = %q, want %q (auto layer should override shared)", got, "2.0.0")
	}
	if _, ok := snapA.jvmVersions["auto-tool"]; !ok {
		t.Errorf("auto-tool app missing; jvmVersions=%v", snapA.jvmVersions)
	}
	if snapA.editorconfig != "from-shared\nfrom-auto" {
		t.Errorf("editorconfig = %q, want %q", snapA.editorconfig, "from-shared\nfrom-auto")
	}
	sharedIdx := slices.Index(snapA.ignoreRules, "from-shared: eslint")
	autoIdx := slices.Index(snapA.ignoreRules, "from-auto: prettier")
	if sharedIdx < 0 || autoIdx < 0 {
		t.Fatalf("missing expected ignoreRules: %v", snapA.ignoreRules)
	}
	if sharedIdx >= autoIdx {
		t.Errorf("shared rule (idx=%d) should precede auto rule (idx=%d)", sharedIdx, autoIdx)
	}
}

// TestBeforeConfigsRootOnlyNoChaining verifies that getBeforeConfigs() is read
// only from the auto-discovered git-root config. A declared before-config that
// itself exports getBeforeConfigs() must NOT have its nested declaration
// honoured (no chaining), so the nested file's contribution is absent.
func TestBeforeConfigsRootOnlyNoChaining(t *testing.T) {
	root := setupGitRoot(t)

	nestedPath := filepath.Join(root, "nested.js")
	writeFile(t, nestedPath, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-nested: hadolint"] }; }`)

	sharedPath := filepath.Join(root, "shared.js")
	// shared declares getBeforeConfigs -> nested; this must be ignored.
	writeFile(t, sharedPath, `
function getBeforeConfigs() { return [{ path: "./nested.js" }]; }
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-shared: eslint"] }; }`)

	autoPath := filepath.Join(root, ldflags.PackageName+".config.js")
	writeFile(t, autoPath, `
function getBeforeConfigs() { return [{ path: "./shared.js" }]; }
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-auto: prettier"] }; }`)

	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, false, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	if !slices.Contains(cfg.IgnoreRules, "from-shared: eslint") {
		t.Errorf("declared before-config (shared) should be loaded; rules=%v", cfg.IgnoreRules)
	}
	if !slices.Contains(cfg.IgnoreRules, "from-auto: prettier") {
		t.Errorf("auto config should be loaded; rules=%v", cfg.IgnoreRules)
	}
	if slices.Contains(cfg.IgnoreRules, "from-nested: hadolint") {
		t.Errorf("nested getBeforeConfigs() must NOT be honoured (no chaining); rules=%v", cfg.IgnoreRules)
	}
}

// TestBeforeConfigsFlagOverridesDeclared verifies the precedence rule: when an
// explicit --before-config path is passed, the auto config's getBeforeConfigs()
// declaration is not consulted at all (the flag wins, avoiding a double-load of
// the shared config when the pnpm wrapper supplies --before-config).
func TestBeforeConfigsFlagOverridesDeclared(t *testing.T) {
	root := setupGitRoot(t)

	declaredPath := filepath.Join(root, "declared.js")
	writeFile(t, declaredPath, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-declared: eslint"] }; }`)

	autoPath := filepath.Join(root, ldflags.PackageName+".config.js")
	writeFile(t, autoPath, `
function getBeforeConfigs() { return [{ path: "./declared.js" }]; }
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-auto: prettier"] }; }`)

	flagDir := t.TempDir()
	flagPath := filepath.Join(flagDir, "flag.js")
	writeFile(t, flagPath, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-flag: shellcheck"] }; }`)

	// --before-config flag set -> getBeforeConfigs() must be skipped entirely.
	cfg, _, _, err := loadConfigWithPaths(context.Background(), []string{flagPath}, false, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	if !slices.Contains(cfg.IgnoreRules, "from-flag: shellcheck") {
		t.Errorf("--before-config flag config should be loaded; rules=%v", cfg.IgnoreRules)
	}
	if !slices.Contains(cfg.IgnoreRules, "from-auto: prettier") {
		t.Errorf("auto config should still be loaded; rules=%v", cfg.IgnoreRules)
	}
	if slices.Contains(cfg.IgnoreRules, "from-declared: eslint") {
		t.Errorf("declared before-config must be skipped when --before-config flag is set (no double-load); rules=%v", cfg.IgnoreRules)
	}
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
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-local",
		content: localContent,
	}, resolved, stack, loadConfigOptions{})
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
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-local",
		content: localContent,
	}, resolved, stack, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-missing-hash",
		content: localContent,
	}, resolved, stack, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-circular",
		content: localContent,
	}, resolved, stack, loadConfigOptions{})
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
	// Node D is a shared dependency — should be processed twice (not a cycle).
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
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-diamond",
		content: localContent,
	}, resolved, stack, loadConfigOptions{})
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
	), 0o644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "override.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-override: prettier"] }; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(
		context.Background(),
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
	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, nil)
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
	e, err := engine.New(context.Background(), "")
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
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-skip-remote",
		content: localContent,
	}, resolved, stack, loadConfigOptions{})
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
	if err := os.WriteFile(beforePath, []byte(beforeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	oldSkip := SkipRemoteConfig
	SkipRemoteConfig = false
	defer func() { SkipRemoteConfig = oldSkip }()

	cfg, _, _, err := loadConfigWithPaths(context.Background(), []string{beforePath}, true, nil)
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
	if slices.Contains(urlsCopy, expectedURL) {
		found = true
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
	), 0o644); err != nil {
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
	), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(
		context.Background(),
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

func TestSharedStorageDefaultEntries(t *testing.T) {
	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}
	if len(cfg.SharedStorage) != 2 {
		t.Errorf("SharedStorage should have 2 entries by default, got %d: %v", len(cfg.SharedStorage), cfg.SharedStorage)
	}
	if _, ok := cfg.SharedStorage["datamitsu-agent-prompt"]; !ok {
		t.Errorf("SharedStorage should contain datamitsu-agent-prompt by default")
	}
	if _, ok := cfg.SharedStorage["pnpm-workspace-defaults"]; !ok {
		t.Errorf("SharedStorage should contain pnpm-workspace-defaults by default")
	}
}

// TestSharedStoragePNPMWorkspaceDefaultsRoundTrip verifies that the full config
// chain (Go engine injection → bundled config.js → YAML.stringify → sharedStorage)
// produces a "pnpm-workspace-defaults" entry whose parsed YAML matches the
// single Go-side source in pnpmdefaults.Defaults(). This pins the contract
// removed in Task 2 (the hardcoded JS copy is gone, so the JS now reads the
// injected global).
func TestSharedStoragePNPMWorkspaceDefaultsRoundTrip(t *testing.T) {
	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}

	rawYAML, ok := cfg.SharedStorage["pnpm-workspace-defaults"]
	if !ok {
		t.Fatal(`sharedStorage["pnpm-workspace-defaults"] missing`)
	}
	if rawYAML == "" {
		t.Fatal(`sharedStorage["pnpm-workspace-defaults"] is empty`)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(rawYAML), &parsed); err != nil {
		t.Fatalf("yaml.Unmarshal error = %v\nyaml: %s", err, rawYAML)
	}

	want := pnpmdefaults.Defaults()
	if len(parsed) != len(want) {
		t.Errorf("parsed has %d keys, want %d (parsed: %#v)", len(parsed), len(want), parsed)
	}
	for key, wantVal := range want {
		gotVal, present := parsed[key]
		if !present {
			t.Errorf("key %q missing from round-tripped YAML; parsed: %#v", key, parsed)
			continue
		}
		if !sharedStorageYAMLEqual(gotVal, wantVal) {
			t.Errorf("key %q: got %#v (%T), want %#v (%T)", key, gotVal, gotVal, wantVal, wantVal)
		}
	}
}

// sharedStorageYAMLEqual compares values after YAML round-trip, where integer
// literals may come back as uint64/int64/float64 depending on go-yaml's parser
// path. The Go-side source uses `int`, so we normalize to int64 for compares.
func sharedStorageYAMLEqual(got, want any) bool {
	if got == want {
		return true
	}
	gi, gok := yamlToInt64(got)
	wi, wok := yamlToInt64(want)
	if gok && wok {
		return gi == wi
	}
	return false
}

func yamlToInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		return int64(x), true
	case float64:
		if float64(int64(x)) == x {
			return int64(x), true
		}
	}
	return 0, false
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
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-cache-on-disk",
		content: localContent,
	}, resolved, stack, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-repeat-1",
		content: localContent,
	}, resolved1, stack1, loadConfigOptions{})
	if err != nil {
		t.Fatalf("first processConfigSource error: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("first call: expected 1 HTTP request, got %d", requestCount)
	}

	// Second call — should use cache, no additional HTTP request
	resolved2 := make(map[string]bool)
	stack2 := make(map[string]bool)
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-repeat-2",
		content: localContent,
	}, resolved2, stack2, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-cache-1",
		content: localContent,
	}, resolved1, stack1, loadConfigOptions{})
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Server goes down — cached content hash still matches, so cache hit
	serverDown = true

	resolved2 := make(map[string]bool)
	stack2 := make(map[string]bool)
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-cache-2",
		content: localContent,
	}, resolved2, stack2, loadConfigOptions{})
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
	result, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-with-min-version",
		content: content,
	}, resolved, stack, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-no-min-version",
		content: content,
	}, resolved, stack, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-non-string-version",
		content: content,
	}, resolved, stack, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-empty-version",
		content: content,
	}, resolved, stack, loadConfigOptions{})
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
	_, _, err := processConfigSource(context.Background(), nil, configSource{
		name:    "test-invalid-semver",
		content: content,
	}, resolved, stack, loadConfigOptions{})
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
	), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
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

// withTestVersion overrides the build version for the duration of a test so
// version-check enforcement can be exercised. The default "dev" build satisfies
// any minVersion, so failure-path tests must pin a concrete stable version.
func withTestVersion(t *testing.T, v string) {
	t.Helper()
	orig := ldflags.Version
	ldflags.Version = v
	t.Cleanup(func() { ldflags.Version = orig })
}

func TestLoadConfigWithHighMinVersionFails(t *testing.T) {
	// Config with getMinVersion="99.0.0" must fail because the pinned current
	// version (v1.0.0) is less than required.
	withTestVersion(t, "1.0.0")
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "high-version.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return {}; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
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
	), 0o644); err != nil {
		t.Fatal(err)
	}

	// ldflags.Version defaults to "dev" which normalizes to v0.0.0
	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
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
	), 0o644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-config: prettier"] }; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(context.Background(), []string{beforePath}, true, []string{configPath})
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
	withTestVersion(t, "1.0.0")
	beforeDir := t.TempDir()
	beforePath := filepath.Join(beforeDir, "before.js")
	if err := os.WriteFile(beforePath, []byte(
		`function getMinVersion() { return "0.0.0"; }
function getConfig(input) { return { ignoreRules: ["from-before: eslint"] }; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "failing-config.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return {}; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := loadConfigWithPaths(context.Background(), []string{beforePath}, true, []string{configPath})
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
	withTestVersion(t, "1.0.0")
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "my-special-config.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return {}; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
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
	e, err := engine.New(context.Background(), "")
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
			_, _, err := processConfigSource(context.Background(), nil, configSource{
				name:    "test-extraction-" + tt.name,
				content: content,
			}, resolved, stack, loadConfigOptions{})
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
	_, layerMap, _, err := loadConfigWithPaths(context.Background(), nil, true, nil)
	if err != nil {
		t.Fatalf("loadConfigWithPaths error: %v", err)
	}
	if layerMap == nil {
		t.Fatal("layerMap should not be nil")
	}
}

func TestLoadConfigImplReturns4Tuple(t *testing.T) {
	cfg, layerMap, vm, err := loadConfigWithPaths(context.Background(), nil, true, nil)
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
        setup: {
            ".editorconfig": {
                scope: "git-root",
                content: function(context) { return "root = true"; }
            }
        }
    };
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, layerMap, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
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
        setup: {
            ".editorconfig": {
                scope: "git-root",
                content: function(context) { return "from-before"; }
            }
        }
    };
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "override.js")
	if err := os.WriteFile(configPath, []byte(`
function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
    return {
        setup: {
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
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, layerMap, _, err := loadConfigWithPaths(context.Background(), []string{beforePath}, true, []string{configPath})
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
        setup: {
            "test-fail-eval.txt": {
                scope: "git-root",
                content: function(context) { throw new Error("content generation failed"); }
            }
        }
    };
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, layerMap, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
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

// Tests for unstable version bypass in config loader (Task 3)

func TestLoadConfigUnstableVersionBypassesHighMinVersion(t *testing.T) {
	// When the binary is built as an unstable release, the version check
	// must be skipped rather than blocking config loading. The bypass is
	// advisory, so the loader is expected to emit a warning identifying
	// the bypassed source.
	unstableVersion := "0.0.0-unstable.20260523.abcdef0"
	original := ldflags.Version
	ldflags.Version = unstableVersion
	t.Cleanup(func() { ldflags.Version = original })

	observed := swapLoggerWithObserver(t, zapcore.WarnLevel)

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "high-version.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return { ignoreRules: ["unstable-bypass: eslint"] }; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
	if err != nil {
		t.Fatalf("expected unstable bypass to succeed against high minVersion, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("config should not be nil after unstable bypass")
	}

	hasRule := false
	for _, r := range cfg.IgnoreRules {
		if r == "unstable-bypass: eslint" {
			hasRule = true
		}
	}
	if !hasRule {
		t.Errorf("expected ignoreRule from config to load after bypass, got %v", cfg.IgnoreRules)
	}

	entries := observed.FilterMessageSnippet("version check skipped").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 'version check skipped' warning, got %d (all=%v)", len(entries), observed.All())
	}
	fields := entries[0].ContextMap()
	if fields["current"] != unstableVersion {
		t.Errorf("warning 'current' field = %v, want %q", fields["current"], unstableVersion)
	}
	if fields["required"] != "99.0.0" {
		t.Errorf("warning 'required' field = %v, want %q", fields["required"], "99.0.0")
	}
	if _, ok := fields["source"]; !ok {
		t.Errorf("warning is missing 'source' field, got fields: %v", fields)
	}
}

func TestLoadConfigStableVersionStillFailsHighMinVersion(t *testing.T) {
	// Sanity-check: a stable build below required version must still error,
	// and must NOT emit the unstable-bypass warning.
	original := ldflags.Version
	ldflags.Version = "0.0.1"
	t.Cleanup(func() { ldflags.Version = original })

	observed := swapLoggerWithObserver(t, zapcore.WarnLevel)

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "stable-high-version.js")
	if err := os.WriteFile(configPath, []byte(
		`function getMinVersion() { return "99.0.0"; }
function getConfig(input) { return {}; }`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{configPath})
	if err == nil {
		t.Fatal("expected stable version below required to fail")
	}
	if !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("error should retain upgrade instructions for stable builds, got: %v", err)
	}
	if n := observed.FilterMessageSnippet("version check skipped").Len(); n != 0 {
		t.Errorf("stable failure path emitted %d unexpected unstable-bypass warnings", n)
	}
}
