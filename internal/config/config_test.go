package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/dop251/goja"
	"github.com/goccy/go-yaml"
)

func TestGetDefaultConfig(t *testing.T) {
	config, err := GetDefaultConfig()
	if err != nil {
		t.Fatalf("GetDefaultConfig() error = %v", err)
	}

	if config == "" {
		t.Error("GetDefaultConfig() returned empty string")
	}
}

func TestGetDefaultConfigDTS(t *testing.T) {
	dts := GetDefaultConfigDTS()

	if dts == "" {
		t.Error("GetDefaultConfigDTS() returned empty string")
	}
}

func TestDefaultConfigPNPMWorkspaceDefaults(t *testing.T) {
	configScript, err := GetDefaultConfig()
	if err != nil {
		t.Fatalf("GetDefaultConfig() error = %v", err)
	}

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	yamlNamespace := map[string]any{
		"stringify": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewTypeError("YAML.stringify requires at least 1 argument"))
			}
			exported := call.Argument(0).Export()
			bytes, err := yaml.Marshal(exported)
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("YAML.stringify error: %w", err)))
			}
			return vm.ToValue(string(bytes))
		},
	}
	if err := vm.Set("YAML", yamlNamespace); err != nil {
		t.Fatalf("vm.Set YAML error: %v", err)
	}

	// Mirror what internal/engine does: inject the pnpm defaults global so the
	// compiled config.js can read it instead of hardcoding its own copy.
	if err := vm.Set("pnpmWorkspaceDefaults", pnpmdefaults.Defaults()); err != nil {
		t.Fatalf("vm.Set pnpmWorkspaceDefaults error: %v", err)
	}

	if _, err := vm.RunString(configScript); err != nil {
		t.Fatalf("RunString(configScript) error = %v", err)
	}

	getConfigVal := vm.Get("getConfig")
	getConfigFn, ok := goja.AssertFunction(getConfigVal)
	if !ok {
		t.Fatal("getConfig is not a function")
	}

	result, err := getConfigFn(goja.Undefined(), vm.NewObject())
	if err != nil {
		t.Fatalf("getConfig() error = %v", err)
	}

	type configShape struct {
		SharedStorage map[string]string `json:"sharedStorage"`
	}
	var cfg configShape
	if err := vm.ExportTo(result, &cfg); err != nil {
		t.Fatalf("ExportTo error = %v", err)
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

	expected := map[string]any{
		"strictDepBuilds":           true,
		"blockExoticSubdeps":        true,
		"enablePrePostScripts":      false,
		"dangerouslyAllowAllBuilds": false,
		"minimumReleaseAge":         uint64(10080),
		"trustPolicy":               "no-downgrade",
		"lockfile":                  true,
		"preferFrozenLockfile":      true,
	}
	for key, want := range expected {
		got, present := parsed[key]
		if !present {
			t.Errorf("key %q missing from pnpm-workspace-defaults YAML; got: %#v", key, parsed)
			continue
		}
		if !equalAny(got, want) {
			t.Errorf("key %q: got %#v (%T), want %#v (%T)", key, got, got, want, want)
		}
	}
	if len(parsed) != len(expected) {
		t.Errorf("parsed has %d keys, want %d; parsed: %#v", len(parsed), len(expected), parsed)
	}
}

// runDefaultConfigForTest executes the embedded default config.js in a goja VM
// (mirroring the globals internal/engine injects) and returns the parsed Config
// produced by getConfig({}). It is the end-to-end harness for asserting the
// embedded defaults the way they are actually evaluated at runtime.
func runDefaultConfigForTest(t *testing.T) *Config {
	t.Helper()

	configScript, err := GetDefaultConfig()
	if err != nil {
		t.Fatalf("GetDefaultConfig() error = %v", err)
	}

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	yamlNamespace := map[string]any{
		"stringify": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewTypeError("YAML.stringify requires at least 1 argument"))
			}
			b, err := yaml.Marshal(call.Argument(0).Export())
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("YAML.stringify error: %w", err)))
			}
			return vm.ToValue(string(b))
		},
	}
	if err := vm.Set("YAML", yamlNamespace); err != nil {
		t.Fatalf("vm.Set YAML error: %v", err)
	}
	if err := vm.Set("pnpmWorkspaceDefaults", pnpmdefaults.Defaults()); err != nil {
		t.Fatalf("vm.Set pnpmWorkspaceDefaults error: %v", err)
	}

	if _, err := vm.RunString(configScript); err != nil {
		t.Fatalf("RunString(configScript) error = %v", err)
	}

	getConfigFn, ok := goja.AssertFunction(vm.Get("getConfig"))
	if !ok {
		t.Fatal("getConfig is not a function")
	}
	result, err := getConfigFn(goja.Undefined(), vm.NewObject())
	if err != nil {
		t.Fatalf("getConfig() error = %v", err)
	}

	cfg := &Config{}
	if err := vm.ExportTo(result, cfg); err != nil {
		t.Fatalf("ExportTo error = %v", err)
	}
	return cfg
}

// decompressLockFileForTest mirrors runtimemanager.DecompressLockFile without
// importing that package (which would create an import cycle: runtimemanager
// imports config). It strips the "br:" prefix, base64-decodes, then brotli
// decompresses.
func decompressLockFileForTest(t *testing.T, data string) string {
	t.Helper()
	const prefix = "br:"
	if !strings.HasPrefix(data, prefix) {
		t.Fatalf("lock file missing %q prefix", prefix)
	}
	compressed, err := base64.StdEncoding.DecodeString(data[len(prefix):])
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	decompressed, err := io.ReadAll(brotli.NewReader(bytes.NewReader(compressed)))
	if err != nil {
		t.Fatalf("brotli decompress error: %v", err)
	}
	return string(decompressed)
}

func TestDefaultConfigGoRuntime(t *testing.T) {
	cfg := runDefaultConfigForTest(t)

	rt, ok := cfg.Runtimes["go"]
	if !ok {
		t.Fatal(`runtimes["go"] missing from default config`)
	}
	if rt.Kind != RuntimeKindGo {
		t.Errorf("go runtime kind = %q, want %q", rt.Kind, RuntimeKindGo)
	}
	if rt.Mode != RuntimeModeManaged {
		t.Errorf("go runtime mode = %q, want %q", rt.Mode, RuntimeModeManaged)
	}
	if rt.Go == nil || rt.Go.GoVersion == "" {
		t.Fatal("go runtime missing goVersion")
	}
	if rt.Managed == nil || rt.Managed.Binaries == nil {
		t.Fatal("go runtime missing managed binaries")
	}

	// Every managed binary MUST carry a SHA-256 hash and URL: the project's
	// security policy forbids hash-less downloads. Verify across all platforms.
	binaryCount := 0
	for osType, archMap := range rt.Managed.Binaries {
		for archType, libcMap := range archMap {
			for libc, info := range libcMap {
				binaryCount++
				if info.Hash == "" {
					t.Errorf("go SDK binary %s/%s/%s missing mandatory hash", osType, archType, libc)
				}
				if info.URL == "" {
					t.Errorf("go SDK binary %s/%s/%s missing url", osType, archType, libc)
				}
				if info.BinaryPath == nil || *info.BinaryPath == "" {
					t.Errorf("go SDK binary %s/%s/%s missing binaryPath", osType, archType, libc)
				}
			}
		}
	}
	if binaryCount == 0 {
		t.Fatal("go runtime has no managed binaries")
	}

	// The plan requires linux/darwin on amd64/arm64.
	required := []struct {
		os   syslist.OsType
		arch syslist.ArchType
	}{
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64},
		{syslist.OsTypeDarwin, syslist.ArchTypeAmd64},
		{syslist.OsTypeDarwin, syslist.ArchTypeArm64},
	}
	for _, r := range required {
		archMap, ok := rt.Managed.Binaries[r.os]
		if !ok {
			t.Errorf("go runtime missing OS %q", r.os)
			continue
		}
		if _, ok := archMap[r.arch]; !ok {
			t.Errorf("go runtime missing %s/%s", r.os, r.arch)
		}
	}
}

func TestDefaultConfigGovulncheckApp(t *testing.T) {
	cfg := runDefaultConfigForTest(t)

	app, ok := cfg.Apps["govulncheck"]
	if !ok {
		t.Fatal(`apps["govulncheck"] missing from default config`)
	}
	if app.Go == nil {
		t.Fatal("govulncheck app has no go config")
	}
	if app.Go.PackageName != "golang.org/x/vuln/cmd/govulncheck" {
		t.Errorf("govulncheck packageName = %q, want %q", app.Go.PackageName, "golang.org/x/vuln/cmd/govulncheck")
	}
	if app.Go.Version == "" {
		t.Error("govulncheck app missing version")
	}
	// Lock file is mandatory for Go apps and must be the brotli-compressed wrapper.
	if app.Go.LockFile == "" {
		t.Fatal("govulncheck app has empty lockFile (mandatory for Go apps)")
	}

	wrapper := decompressLockFileForTest(t, app.Go.LockFile)

	var lf struct {
		Mod string `json:"mod"`
		Sum string `json:"sum"`
	}
	if err := json.Unmarshal([]byte(wrapper), &lf); err != nil {
		t.Fatalf("lock file is not valid JSON wrapper: %v\ncontent: %s", err, wrapper)
	}
	if lf.Mod == "" {
		t.Error("lock file wrapper missing go.mod content")
	}
	if lf.Sum == "" {
		t.Error("lock file wrapper missing go.sum content")
	}
	if !strings.Contains(lf.Mod, "golang.org/x/vuln") {
		t.Errorf("go.mod should require golang.org/x/vuln, got:\n%s", lf.Mod)
	}
	if !strings.Contains(lf.Sum, "golang.org/x/vuln") {
		t.Errorf("go.sum should contain golang.org/x/vuln checksums, got:\n%s", lf.Sum)
	}
}

func equalAny(got, want any) bool {
	switch w := want.(type) {
	case uint64:
		switch g := got.(type) {
		case uint64:
			return g == w
		case int64:
			return g >= 0 && uint64(g) == w
		case int:
			return g >= 0 && uint64(g) == w
		case float64:
			return uint64(g) == w
		}
		return false
	default:
		return got == want
	}
}

func TestStripTypes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "simple typescript",
			input:   "const x: number = 42;",
			wantErr: false,
		},
		{
			name:    "function with types",
			input:   "function add(a: number, b: number): number { return a + b; }",
			wantErr: false,
		},
		{
			name:    "plain javascript",
			input:   "const x = 42;",
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := StripTypes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("StripTypes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && result == "" && tt.input != "" {
				t.Error("StripTypes() returned empty string for non-empty input")
			}
		})
	}
}

func TestToolScopeConstants(t *testing.T) {
	if ToolScopePerFile != "per-file" {
		t.Errorf("ToolScopePerFile = %q, want %q", ToolScopePerFile, "per-file")
	}

	if ToolScopeRepository != "repository" {
		t.Errorf("ToolScopeRepository = %q, want %q", ToolScopeRepository, "repository")
	}

	if ToolScopePerProject != "per-project" {
		t.Errorf("ToolScopePerProject = %q, want %q", ToolScopePerProject, "per-project")
	}
}

func TestOperationTypeConstants(t *testing.T) {
	if OpFix != "fix" {
		t.Errorf("OpFix = %q, want %q", OpFix, "fix")
	}

	if OpLint != "lint" {
		t.Errorf("OpLint = %q, want %q", OpLint, "lint")
	}
}

func TestRuntimeKindConstants(t *testing.T) {
	if RuntimeKindUV != "uv" {
		t.Errorf("RuntimeKindUV = %q, want %q", RuntimeKindUV, "uv")
	}
	if RuntimeKindNode != "node" {
		t.Errorf("RuntimeKindNode = %q, want %q", RuntimeKindNode, "node")
	}
	if RuntimeKindJVM != "jvm" {
		t.Errorf("RuntimeKindJVM = %q, want %q", RuntimeKindJVM, "jvm")
	}
	if RuntimeKindGo != "go" {
		t.Errorf("RuntimeKindGo = %q, want %q", RuntimeKindGo, "go")
	}
}

func TestRuntimeConfigGo_Fields(t *testing.T) {
	cfg := RuntimeConfigGo{
		GoVersion: "1.24.3",
	}

	if cfg.GoVersion != "1.24.3" {
		t.Errorf("GoVersion = %q, want %q", cfg.GoVersion, "1.24.3")
	}
}

func TestRuntimeConfig_GoField_JSONRoundTrip(t *testing.T) {
	original := RuntimeConfig{
		Kind: RuntimeKindGo,
		Mode: RuntimeModeManaged,
		Go: &RuntimeConfigGo{
			GoVersion: "1.24.3",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	dataStr := string(data)
	if !strings.Contains(dataStr, `"goVersion"`) {
		t.Errorf("JSON should contain 'goVersion' field, got: %s", dataStr)
	}

	var decoded RuntimeConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if decoded.Kind != RuntimeKindGo {
		t.Errorf("Kind = %q, want %q", decoded.Kind, RuntimeKindGo)
	}
	if decoded.Go == nil {
		t.Fatal("decoded.Go is nil after round-trip")
	}
	if decoded.Go.GoVersion != "1.24.3" {
		t.Errorf("GoVersion = %q, want %q", decoded.Go.GoVersion, "1.24.3")
	}
}

func TestRuntimeConfig_GoField_OmittedWhenNil(t *testing.T) {
	rc := RuntimeConfig{
		Kind: RuntimeKindUV,
		Mode: RuntimeModeManaged,
	}

	data, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	if strings.Contains(string(data), `"go"`) {
		t.Errorf("JSON should omit nil go field, got: %s", string(data))
	}
}

func TestRuntimeConfigNode_Fields(t *testing.T) {
	cfg := RuntimeConfigNode{
		NodeVersion: "26.2.0",
		PNPMVersion: "11.0.0",
		PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	}

	if cfg.NodeVersion != "26.2.0" {
		t.Errorf("NodeVersion = %q, want %q", cfg.NodeVersion, "26.2.0")
	}
	if cfg.PNPMVersion != "11.0.0" {
		t.Errorf("PNPMVersion = %q, want %q", cfg.PNPMVersion, "11.0.0")
	}
	if cfg.PNPMHash != "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" {
		t.Errorf("PNPMHash = %q, unexpected", cfg.PNPMHash)
	}
}

func TestRuntimeConfig_NodeField_JSONRoundTrip(t *testing.T) {
	jsonStr := `{
		"kind": "node",
		"mode": "managed",
		"node": {
			"nodeVersion": "26.2.0",
			"pnpmVersion": "11.0.0",
			"pnpmHash": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
		}
	}`

	var decoded RuntimeConfig
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if decoded.Kind != RuntimeKindNode {
		t.Errorf("Kind = %q, want %q", decoded.Kind, RuntimeKindNode)
	}
	if decoded.Node == nil {
		t.Fatal("decoded.Node is nil after parsing")
	}
	if decoded.Node.NodeVersion != "26.2.0" {
		t.Errorf("NodeVersion = %q, want %q", decoded.Node.NodeVersion, "26.2.0")
	}
	if decoded.Node.PNPMVersion != "11.0.0" {
		t.Errorf("PNPMVersion = %q, want %q", decoded.Node.PNPMVersion, "11.0.0")
	}
	if decoded.Node.PNPMHash != "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" {
		t.Errorf("PNPMHash = %q, unexpected", decoded.Node.PNPMHash)
	}

	// Re-marshal and ensure the node field is emitted.
	data, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	if !strings.Contains(string(data), `"node"`) {
		t.Errorf("JSON should contain 'node' field, got: %s", string(data))
	}
}

func TestRuntimeConfig_NodeField_OmittedWhenNil(t *testing.T) {
	rc := RuntimeConfig{
		Kind: RuntimeKindUV,
		Mode: RuntimeModeManaged,
	}

	data, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	if strings.Contains(string(data), `"node"`) {
		t.Errorf("JSON should omit nil node field, got: %s", string(data))
	}
}

// TestWorkingDirConstants removed - WorkingDir type no longer exists

func TestProjectType(t *testing.T) {
	pt := ProjectType{
		Markers:     []string{"package.json", "pnpm-lock.yaml"},
		Description: "Node.js project",
	}

	if len(pt.Markers) != 2 {
		t.Errorf("len(Markers) = %d, want 2", len(pt.Markers))
	}

	if pt.Description != "Node.js project" {
		t.Errorf("Description = %q, want %q", pt.Description, "Node.js project")
	}
}

func TestToolOperation(t *testing.T) {
	batchFalse := false
	op := ToolOperation{
		App:      "eslint",
		Args:     []string{"--fix"},
		Scope:    ToolScopePerFile,
		Batch:    &batchFalse,
		Globs:    []string{"*.js", "*.ts"},
		Priority: 10,
	}

	if op.App != "eslint" {
		t.Errorf("App = %q, want %q", op.App, "eslint")
	}

	if len(op.Args) != 1 {
		t.Errorf("len(Args) = %d, want 1", len(op.Args))
	}

	if op.Scope != ToolScopePerFile {
		t.Errorf("Scope = %q, want %q", op.Scope, ToolScopePerFile)
	}
}

func TestToolOperationAppJSONMarshal(t *testing.T) {
	op := ToolOperation{
		App:   "golangci-lint",
		Args:  []string{"run", "--fix"},
		Scope: ToolScopeRepository,
		Globs: []string{"**/*.go"},
	}

	data, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	dataStr := string(data)
	if !strings.Contains(dataStr, `"app"`) {
		t.Errorf("JSON should contain 'app' field, got: %s", dataStr)
	}
	if strings.Contains(dataStr, `"command"`) {
		t.Errorf("JSON should not contain 'command' field, got: %s", dataStr)
	}

	var parsed ToolOperation
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if parsed.App != "golangci-lint" {
		t.Errorf("parsed App = %q, want %q", parsed.App, "golangci-lint")
	}
	if len(parsed.Args) != 2 {
		t.Errorf("parsed len(Args) = %d, want 2", len(parsed.Args))
	}
}

func TestToolOperationAppJSONUnmarshal(t *testing.T) {
	jsonStr := `{"app":"prettier","args":["--write"],"scope":"per-file","globs":["**/*.ts"]}`

	var op ToolOperation
	if err := json.Unmarshal([]byte(jsonStr), &op); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if op.App != "prettier" {
		t.Errorf("App = %q, want %q", op.App, "prettier")
	}
}

func TestTool(t *testing.T) {
	tool := Tool{
		Name:         "eslint",
		ProjectTypes: []string{"node"},
		Operations: map[OperationType]ToolOperation{
			OpLint: {
				App:   "eslint",
				Scope: ToolScopePerFile,
			},
			OpFix: {
				App:   "eslint",
				Args:  []string{"--fix"},
				Scope: ToolScopePerFile,
			},
		},
	}

	if tool.Name != "eslint" {
		t.Errorf("Name = %q, want %q", tool.Name, "eslint")
	}

	if len(tool.Operations) != 2 {
		t.Errorf("len(Operations) = %d, want 2", len(tool.Operations))
	}

	lintOp, exists := tool.Operations[OpLint]
	if !exists {
		t.Error("lint operation does not exist")
	}

	if lintOp.App != "eslint" {
		t.Errorf("lint App = %q, want %q", lintOp.App, "eslint")
	}
}

func TestToolSkipFields(t *testing.T) {
	tool := Tool{
		Name:       "trufflehog",
		Skip:       true,
		SkipReason: "runs in CI only",
		Operations: map[OperationType]ToolOperation{
			OpLint: {App: "trufflehog", Scope: ToolScopeRepository},
		},
	}
	if !tool.Skip {
		t.Error("Skip = false, want true")
	}
	if tool.SkipReason != "runs in CI only" {
		t.Errorf("SkipReason = %q, want %q", tool.SkipReason, "runs in CI only")
	}
}

func TestToolSkipJSONRoundTrip(t *testing.T) {
	in := Tool{Name: "trufflehog", Skip: true, SkipReason: "runs in CI only"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"skip":true`) || !strings.Contains(string(data), `"skipReason":"runs in CI only"`) {
		t.Errorf("marshaled JSON missing skip fields: %s", data)
	}
	var out Tool
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Skip || out.SkipReason != "runs in CI only" {
		t.Errorf("round-trip lost skip fields: %+v", out)
	}
}

// Unset skip fields must be omitted so configs that don't use skip serialize
// identically (keeping cache keys stable).
func TestToolSkipJSONOmitEmpty(t *testing.T) {
	data, err := json.Marshal(Tool{Name: "eslint"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "skip") {
		t.Errorf("expected skip fields omitted when unset, got: %s", data)
	}
}

func TestConfigSetup(t *testing.T) {
	setup := ConfigSetup{
		ProjectTypes:      []string{"node"},
		Scope:             ScopeGitRoot,
		OtherFileNameList: []string{".eslintrc.js", ".eslintrc.json"},
		DeleteOnly:        false,
	}

	if len(setup.ProjectTypes) != 1 {
		t.Errorf("len(ProjectTypes) = %d, want 1", len(setup.ProjectTypes))
	}

	if setup.Scope != ScopeGitRoot {
		t.Errorf("Scope = %q, want %q", setup.Scope, ScopeGitRoot)
	}

	if len(setup.OtherFileNameList) != 2 {
		t.Errorf("len(OtherFileNameList) = %d, want 2", len(setup.OtherFileNameList))
	}

	if setup.DeleteOnly {
		t.Error("DeleteOnly should be false")
	}
}

func TestConfigSetupLinkTarget(t *testing.T) {
	ci := ConfigSetup{
		Scope:      ScopeGitRoot,
		LinkTarget: "AGENTS.md",
	}

	if ci.LinkTarget != "AGENTS.md" {
		t.Errorf("LinkTarget = %q, want %q", ci.LinkTarget, "AGENTS.md")
	}
	if ci.Scope != ScopeGitRoot {
		t.Errorf("Scope = %q, want %q", ci.Scope, ScopeGitRoot)
	}
}

func TestConfigSetupLinkTargetEmpty(t *testing.T) {
	ci := ConfigSetup{
		Scope: ScopeGitRoot,
	}

	if ci.LinkTarget != "" {
		t.Errorf("LinkTarget = %q, want empty string", ci.LinkTarget)
	}
}

func TestConfigSetupLinkTargetJSON(t *testing.T) {
	ci := ConfigSetup{
		Scope:      ScopeGitRoot,
		LinkTarget: "../AGENTS.md",
	}

	data, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var parsed ConfigSetup
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if parsed.LinkTarget != "../AGENTS.md" {
		t.Errorf("parsed LinkTarget = %q, want %q", parsed.LinkTarget, "../AGENTS.md")
	}
	if parsed.Scope != ScopeGitRoot {
		t.Errorf("parsed Scope = %q, want %q", parsed.Scope, ScopeGitRoot)
	}
}

func TestConfigSetupLinkTargetJSONOmitEmpty(t *testing.T) {
	ci := ConfigSetup{
		Scope: ScopeGitRoot,
	}

	data, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	dataStr := string(data)
	if strings.Contains(dataStr, "linkTarget") {
		t.Errorf("JSON should omit empty linkTarget, got: %s", dataStr)
	}
}

func TestConfigSetupTools(t *testing.T) {
	ci := ConfigSetup{
		Tools: []string{"golangci-lint"},
	}

	if len(ci.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(ci.Tools))
	}
	if ci.Tools[0] != "golangci-lint" {
		t.Errorf("Tools[0] = %q, want %q", ci.Tools[0], "golangci-lint")
	}
}

func TestConfigSetupToolsJSON(t *testing.T) {
	ci := ConfigSetup{
		Tools: []string{"golangci-lint", "prettier"},
	}

	data, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var parsed ConfigSetup
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if len(parsed.Tools) != 2 || parsed.Tools[0] != "golangci-lint" || parsed.Tools[1] != "prettier" {
		t.Errorf("parsed Tools = %v, want [golangci-lint prettier]", parsed.Tools)
	}
}

func TestConfigSetupToolsJSONOmitEmpty(t *testing.T) {
	ci := ConfigSetup{
		Scope: ScopeGitRoot,
	}

	data, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	if strings.Contains(string(data), "tools") {
		t.Errorf("JSON should omit empty tools, got: %s", string(data))
	}
}

// TestConfigSetupToolsFromJS guards the real data flow: a JS `setup` entry's
// `tools` array must populate ConfigSetup.Tools via the same goja json field
// mapper + ExportTo path the config loader uses (no special-case extraction).
func TestConfigSetupToolsFromJS(t *testing.T) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	val, err := vm.RunString(`({ projectTypes: ["go"], tools: ["golangci-lint"] })`)
	if err != nil {
		t.Fatalf("RunString error: %v", err)
	}

	var ci ConfigSetup
	if err := vm.ExportTo(val, &ci); err != nil {
		t.Fatalf("ExportTo error: %v", err)
	}

	if len(ci.Tools) != 1 || ci.Tools[0] != "golangci-lint" {
		t.Errorf("Tools = %v, want [golangci-lint]", ci.Tools)
	}
	if len(ci.ProjectTypes) != 1 || ci.ProjectTypes[0] != "go" {
		t.Errorf("ProjectTypes = %v, want [go]", ci.ProjectTypes)
	}
}
