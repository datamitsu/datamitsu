package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/logger"
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

	c, err := cache.NewCache(cacheDir, projectPath, config.Config{}, map[string][]string{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	e := NewExecutor(projectPath, false, false, nil, c)

	task := Task{
		ToolName:  "gofmt",
		Operation: config.OpLint,
		Files:     []string{file},
		OpConfig:  config.ToolOperation{Scope: config.ToolScopePerFile},
	}

	// Nothing cached yet → the file is returned for processing.
	if got := e.filterFilesByCache(task); len(got) != 1 {
		t.Fatalf("uncached filter = %v, want the one file", got)
	}

	// Record success, then the same file is filtered out (cache hit).
	e.updateCacheAfterSuccess(task, task.Files)
	if got := e.filterFilesByCache(task); len(got) != 0 {
		t.Errorf("post-success filter = %v, want empty (cache hit)", got)
	}

	// With caching disabled for the tool, the file passes through again.
	disabled := false
	task.OpConfig.Cache = &disabled
	if got := e.filterFilesByCache(task); len(got) != 1 {
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
	plan := &ExecutionPlan{Groups: []TaskGroup{
		{Tasks: []Task{{ToolName: "beta"}, {ToolName: "alpha"}}},
		{Tasks: []Task{{ToolName: "alpha"}, {ToolName: "gamma"}}}, // duplicate alpha
	}}
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
	plan := &ExecutionPlan{Groups: []TaskGroup{{Tasks: []Task{repoTask("a"), repoTask("b")}}}}
	out := NewDetailedFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "2 parallel groups") {
		t.Errorf("detailed multi-group line missing: %s", out)
	}
}
