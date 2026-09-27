package config

import (
	"strings"
	"testing"
)

func validateOp(op ToolOperation) error {
	op.App, op.Scope = "x", ToolScopeRepository
	return ValidateTools(MapOfTools{"tool": {Operations: map[OperationType]ToolOperation{OpLint: op}}}, nil)
}

func TestValidateToolsInheritEnv(t *testing.T) {
	tests := []struct {
		name    string
		op      ToolOperation
		wantErr string
	}{
		{"a stripped name", ToolOperation{InheritEnv: []string{"GITHUB_ACTIONS"}}, ""},
		{"a name nothing strips", ToolOperation{InheritEnv: []string{"MY_TOOL_MODE", "_X1"}}, ""},
		{"lower case", ToolOperation{InheritEnv: []string{"github_actions"}}, "not a variable name"},
		{"leading digit", ToolOperation{InheritEnv: []string{"1PASSWORD"}}, "not a variable name"},
		{"an assignment", ToolOperation{InheritEnv: []string{"CI=true"}}, "not a variable name"},
		{"empty", ToolOperation{InheritEnv: []string{""}}, "not a variable name"},
		{"duplicate", ToolOperation{InheritEnv: []string{"AI_AGENT", "AI_AGENT"}}, "listed more than once"},
		{"NO_COLOR", ToolOperation{InheritEnv: []string{"NO_COLOR"}}, "cannot be inherited"},
		{"PATH", ToolOperation{InheritEnv: []string{"PATH"}}, "PATH cannot be inherited"},
		{"datamitsu's own", ToolOperation{InheritEnv: []string{"DATAMITSU_CACHE_DIR"}}, "datamitsu's own variables cannot be named"},
		{"NO_COLOR in env", ToolOperation{Env: map[string]string{"NO_COLOR": ""}}, `env "NO_COLOR"`},
		{"NO_COLOR in env, any case", ToolOperation{Env: map[string]string{"No_Color": "1"}}, `env "No_Color"`},
		{"other env", ToolOperation{Env: map[string]string{"GITHUB_ACTIONS": "false"}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOp(tt.op)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("ValidateTools = %v, want it accepted", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("ValidateTools accepted %+v, want an error mentioning %q", tt.op, tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("ValidateTools = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateToolsInheritEnvReportsEveryName(t *testing.T) {
	err := validateOp(ToolOperation{InheritEnv: []string{"PATH", "NO_COLOR", "ok"}})
	if err == nil {
		t.Fatal("ValidateTools accepted three invalid names")
	}
	for _, name := range []string{`"PATH"`, `"NO_COLOR"`, `"ok"`} {
		if !strings.Contains(err.Error(), "inheritEnv "+name) {
			t.Errorf("ValidateTools = %v, want it to name %s", err, name)
		}
	}
}
