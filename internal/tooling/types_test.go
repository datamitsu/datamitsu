package tooling

import (
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	zerovalue_toolenv "github.com/datamitsu/datamitsu/internal/toolenv"
)

func TestExecutionPlanGetAppNames(t *testing.T) {
	plan := &ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil,
		Groups: []TaskGroup{
			{
				Priority: 0, Tasks: []Task{
					{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", OpConfig: config.ToolOperation{Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "eslint"}},
					{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", OpConfig: config.ToolOperation{Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "prettier"}},
					{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", OpConfig: config.ToolOperation{Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "eslint"}}, // duplicate
				},
			},
			{
				Priority: 0, Tasks: []Task{
					{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", OpConfig: config.ToolOperation{Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "golangci-lint"}},
					{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", OpConfig: config.ToolOperation{Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: ""}}, // empty skipped
				},
			},
		},
	}

	got := plan.GetAppNames()
	want := []string{"eslint", "golangci-lint", "prettier"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetAppNames() = %v, want %v", got, want)
	}
}

func TestExecutionPlanGetAppNamesEmpty(t *testing.T) {
	plan := &ExecutionPlan{ConfigName: "", FailOn: "", Groups: nil, Skipped: nil}
	if got := plan.GetAppNames(); got != nil {
		t.Errorf("GetAppNames() = %v, want nil", got)
	}
}
