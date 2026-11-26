package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// The whole config is marshalled into the cache invalidation key, so a config
// that declares no execution policy must serialize exactly as it did before the
// field existed — otherwise adding it resets every user's cache on upgrade.
func TestExecutionOmittedWhenUnset(t *testing.T) {
	b, err := json.Marshal(Config{Name: "", Version: "", Apps: nil, Bundles: nil, AI: nil, Runtimes: nil, ManagedConfigs: nil, EjectConfigs: nil, ProjectTypes: nil, Execution: nil, InitCommands: nil, IgnoreRules: nil, SharedStorage: nil, OCI: nil, Parsers: nil, Lsp: nil, Tools: MapOfTools{}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "execution") {
		t.Errorf("execution leaked into the marshalled config: %s", b)
	}
}

func TestResolveWidenTo(t *testing.T) {
	tests := []struct {
		name     string
		exec     *Execution
		override WidenTo
		want     WidenTo
	}{
		{"no block", nil, "", DefaultWidenTo},
		{"empty block", &Execution{WidenTo: nil}, "", DefaultWidenTo},
		{"declared", &Execution{WidenTo: map[OperationType]WidenTo{OpFix: WidenToTarget}}, "", WidenToTarget},
		{"declared for another operation", &Execution{WidenTo: map[OperationType]WidenTo{OpLint: WidenToTarget}}, "", DefaultWidenTo},
		// --widen-to was typed for this one run, so it wins in both directions.
		{"override narrows", &Execution{WidenTo: map[OperationType]WidenTo{OpFix: WidenToRepo}}, WidenToTarget, WidenToTarget},
		// Widening is the point of the flag: `dm fix ./one.json --widen-to=repo`
		// is how you ask for the whole-repository tools you just saw skipped.
		{"override widens the declared value", &Execution{WidenTo: map[OperationType]WidenTo{OpFix: WidenToTarget}}, WidenToRepo, WidenToRepo},
		{"override widens the default", nil, WidenToRepo, WidenToRepo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.exec.ResolveWidenTo(OpFix, tt.override); got != tt.want {
				t.Errorf("ResolveWidenTo = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInferGranularity(t *testing.T) {
	tests := []struct {
		name string
		op   ToolOperation
		want ToolGranularity
	}{
		{"declared wins", ToolOperation{App: "", Args: nil, Arity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Granularity: GranularityFile, Scope: ToolScopePerProject}, GranularityFile},
		{"per-file", ToolOperation{App: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: ToolScopePerFile, Args: []string{"{file}"}}, GranularityFile},
		{"repository with a file list", ToolOperation{App: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: ToolScopeRepository, Args: []string{"{files}"}}, GranularityFile},
		{"repository without one", ToolOperation{App: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: ToolScopeRepository, Args: []string{"run"}}, GranularityRepo},
		{"repository scanning a directory", ToolOperation{App: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: ToolScopeRepository, Args: []string{"{target}"}}, GranularityRepo},
		// per-project never infers from args: `ty` is per-project with {files} and
		// shaped exactly like prettier, but its verdict is cross-file.
		{"per-project with a file list", ToolOperation{App: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: ToolScopePerProject, Args: []string{"{files}"}}, GranularityUnit},
		{"per-project without one", ToolOperation{App: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: ToolScopePerProject, Args: []string{"run"}}, GranularityUnit},
		{"unvalidated scope falls back to unit", ToolOperation{App: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: "typo", Args: []string{"{files}"}}, GranularityUnit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferGranularity(tt.op); got != tt.want {
				t.Errorf("InferGranularity = %q, want %q", got, tt.want)
			}
		})
	}
}
