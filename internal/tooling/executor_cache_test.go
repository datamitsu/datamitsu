package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/logger"
	zerovalue_toolenv "github.com/datamitsu/datamitsu/internal/toolenv"
)

func TestExecutorCacheFilterRoundTrip(t *testing.T) {
	cacheDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(projectPath, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := cache.NewCache(cacheDir, projectPath, config.Config{Name: "", Version: "", Apps: nil, Bundles: nil, Runtimes: nil, ManagedConfigs: nil, EjectConfigs: nil, ProjectTypes: nil, Tools: nil, Execution: nil, InitCommands: nil, IgnoreRules: nil, SharedStorage: nil, OCI: nil, Parsers: nil, Lsp: nil}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	e := NewExecutor(projectPath, false, false, nil, c)

	task := Task{
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		ProjectPath:  "",
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     "gofmt",
		Operation:    config.OpLint,
		Files:        []string{file},
		OpConfig:     config.ToolOperation{App: "", Args: nil, Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, Scope: config.ToolScopePerFile},
	}

	// Nothing cached yet → the file is returned for processing.
	got, seen := e.filterFilesByCache(task)
	if len(got) != 1 {
		t.Fatalf("uncached filter = %v, want the one file", got)
	}

	// Record success, then the same file is filtered out (cache hit).
	e.updateCacheAfterSuccess(task, task.Files, seen)
	if got, _ := e.filterFilesByCache(task); len(got) != 0 {
		t.Errorf("post-success filter = %v, want empty (cache hit)", got)
	}

	// With caching disabled for the tool, the file passes through again.
	disabled := false
	task.OpConfig.Cache = &disabled
	if got, _ := e.filterFilesByCache(task); len(got) != 1 {
		t.Errorf("cache-disabled filter = %v, want the file", got)
	}
}

func TestToolNotFoundErrorMessage(t *testing.T) {
	err := &ToolNotFoundError{NotFound: []string{"zzz", "yyy"}, Available: []string{"alpha", "beta"}}
	msg := err.Error()
	for _, want := range []string{"tools not found", "zzz", "yyy", "alpha", "beta"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing %q", msg, want)
		}
	}
}

func TestExecutionPlanGetToolNames(t *testing.T) {
	plan := &ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []TaskGroup{
			{Priority: 0, Tasks: []Task{{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "beta"}, {perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "alpha"}}},
			{Priority: 0, Tasks: []Task{{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "alpha"}, {perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "gamma"}}}, // duplicate alpha
		},
	}
	got := plan.GetToolNames()
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("GetToolNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GetToolNames()[%d] = %q, want %q (sorted, deduped)", i, got[i], want[i])
		}
	}
}

func TestDetailedFormatterParallelizationBranches(t *testing.T) {
	// Two repository-scoped tasks always overlap → multi-group line.
	plan := &ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []TaskGroup{{Priority: 0, Tasks: []Task{repoTask("a"), repoTask("b")}}}}
	out := NewDetailedFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "2 parallel groups") {
		t.Errorf("detailed multi-group line missing: %s", out)
	}
}
