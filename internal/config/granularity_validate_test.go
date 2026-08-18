package config

import (
	"strings"
	"testing"
)

// A typo in a declared granularity or arity must be rejected outright. Both are
// read through maps that return the zero value for anything unrecognised, so an
// unchecked typo silently means "infer it" — the config says one thing and the
// core does another.
func TestValidateToolsRejectsUnknownGranularityAndArity(t *testing.T) {
	tests := []struct {
		name    string
		op      ToolOperation
		wantErr string
	}{
		{
			"unknown granularity",
			ToolOperation{App: "x", Granularity: "package", Scope: ToolScopePerProject},
			"unknown granularity",
		},
		{
			"unknown arity",
			ToolOperation{App: "x", Arity: "several", Scope: ToolScopePerProject, Args: []string{"{files}"}},
			"unknown arity",
		},
		{
			// "file" claims each file's result stands alone, which puts the
			// operation back on the per-file cache. Declaring it for a tool that
			// never receives the file list restores the defect the granularity
			// model exists to remove.
			"file granularity without the file list",
			ToolOperation{App: "x", Granularity: GranularityFile, Scope: ToolScopePerProject, Args: []string{"run"}},
			"needs the file list to reach the tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTools(MapOfTools{
				"tool": {Operations: map[OperationType]ToolOperation{OpLint: tt.op}},
			}, nil)

			if err == nil {
				t.Fatalf("ValidateTools accepted %+v, want an error mentioning %q", tt.op, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateTools = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateToolsAcceptsDeclaredGranularityAndArity(t *testing.T) {
	tests := []struct {
		name string
		op   ToolOperation
	}{
		{"nothing declared", ToolOperation{App: "x", Scope: ToolScopePerProject}},
		{
			"file granularity with the file list",
			ToolOperation{App: "x", Granularity: GranularityFile, Scope: ToolScopePerProject, Args: []string{"{files}"}},
		},
		{
			"file granularity via per-file scope",
			ToolOperation{App: "x", Granularity: GranularityFile, Scope: ToolScopePerFile, Args: []string{"{file}"}},
		},
		{"unit granularity", ToolOperation{App: "x", Granularity: GranularityUnit, Scope: ToolScopePerProject}},
		{"repo granularity", ToolOperation{App: "x", Granularity: GranularityRepo, Scope: ToolScopeRepository}},
		{
			// Declaring the arity is an assertion, so it has to match what the
			// placeholders already imply.
			"arity matching the inferred value",
			ToolOperation{App: "x", Arity: ArityMany, Scope: ToolScopePerProject, Args: []string{"{files}"}},
		},
		{"arity none with no placeholders", ToolOperation{App: "x", Arity: ArityNone, Scope: ToolScopePerProject, Args: []string{"run"}}},
		{"arity dir with a target", ToolOperation{App: "x", Arity: ArityDir, Scope: ToolScopeRepository, Args: []string{"{target}"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateTools(MapOfTools{
				"tool": {Operations: map[OperationType]ToolOperation{OpLint: tt.op}},
			}, nil); err != nil {
				t.Errorf("ValidateTools rejected %+v: %v", tt.op, err)
			}
		})
	}
}

// Declaring an arity is an assertion about argv, not an override of it: the
// placeholders decide the shape, so a mismatch is a config bug the user wants
// told about rather than silently resolved either way.
func TestValidateToolsRejectsAnArityThatContradictsTheArgs(t *testing.T) {
	err := ValidateTools(MapOfTools{
		"tool": {Operations: map[OperationType]ToolOperation{
			OpLint: {App: "x", Arity: ArityOne, Scope: ToolScopePerProject, Args: []string{"{files}"}},
		}},
	}, nil)

	if err == nil {
		t.Fatal("ValidateTools accepted arity \"one\" for args carrying {files}")
	}
	if !strings.Contains(err.Error(), "arity") {
		t.Errorf("ValidateTools = %v, want it to explain the arity mismatch", err)
	}
}
