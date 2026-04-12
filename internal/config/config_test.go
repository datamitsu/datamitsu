package config

import (
	"encoding/json"
	"strings"
	"testing"
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
	if RuntimeKindFNM != "fnm" {
		t.Errorf("RuntimeKindFNM = %q, want %q", RuntimeKindFNM, "fnm")
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

func TestConfigInit(t *testing.T) {
	init := ConfigInit{
		ProjectTypes:      []string{"node"},
		Scope:             ScopeGitRoot,
		OtherFileNameList: []string{".eslintrc.js", ".eslintrc.json"},
		DeleteOnly:        false,
	}

	if len(init.ProjectTypes) != 1 {
		t.Errorf("len(ProjectTypes) = %d, want 1", len(init.ProjectTypes))
	}

	if init.Scope != ScopeGitRoot {
		t.Errorf("Scope = %q, want %q", init.Scope, ScopeGitRoot)
	}

	if len(init.OtherFileNameList) != 2 {
		t.Errorf("len(OtherFileNameList) = %d, want 2", len(init.OtherFileNameList))
	}

	if init.DeleteOnly {
		t.Error("DeleteOnly should be false")
	}
}

func TestConfigInitLinkTarget(t *testing.T) {
	ci := ConfigInit{
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

func TestConfigInitLinkTargetEmpty(t *testing.T) {
	ci := ConfigInit{
		Scope: ScopeGitRoot,
	}

	if ci.LinkTarget != "" {
		t.Errorf("LinkTarget = %q, want empty string", ci.LinkTarget)
	}
}

func TestConfigInitLinkTargetJSON(t *testing.T) {
	ci := ConfigInit{
		Scope:      ScopeGitRoot,
		LinkTarget: "../AGENTS.md",
	}

	data, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var parsed ConfigInit
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

func TestConfigInitLinkTargetJSONOmitEmpty(t *testing.T) {
	ci := ConfigInit{
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
