package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/project"
)

// TestPlannerGetTimings pins that a freshly constructed planner exposes a
// non-nil timings handle (the planner records phase timings during Plan).
func TestPlannerGetTimings(t *testing.T) {
	planner := NewPlanner("/repo", "/repo", nil, config.MapOfTools{}, config.MapOfProjectTypes{}, nil)
	if planner.GetTimings() == nil {
		t.Fatal("GetTimings() = nil, want non-nil timings handle")
	}
}

// TestSkipReasonStringDefault covers the default branch of SkipReason.String,
// which falls back to "config" for any unrecognized value.
func TestSkipReasonStringDefault(t *testing.T) {
	tests := []struct {
		name   string
		reason SkipReason
		want   string
	}{
		{"config", SkipReasonConfig, "config"},
		{"unsupported-platform", SkipReasonUnsupportedPlatform, "unsupported-platform"},
		{"out-of-range falls back to config", SkipReason(99), "config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reason.String(); got != tt.want {
				t.Errorf("SkipReason(%d).String() = %q, want %q", tt.reason, got, tt.want)
			}
		})
	}
}

// TestFilterTasksBySelectedTools covers both the happy path (filtering to the
// selected subset) and the ToolNotFoundError path (unknown tool name).
func TestFilterTasksBySelectedTools(t *testing.T) {
	tools := config.MapOfTools{
		"eslint":     {Name: "eslint"},
		"gofmt":      {Name: "gofmt"},
		"shellcheck": {Name: "shellcheck"},
	}
	planner := NewPlanner("/repo", "/repo", nil, tools, config.MapOfProjectTypes{}, nil)

	tasks := []Task{
		{ToolName: "eslint"},
		{ToolName: "gofmt"},
		{ToolName: "shellcheck"},
	}

	t.Run("filters to selected subset", func(t *testing.T) {
		got, err := planner.filterTasksBySelectedTools(tasks, []string{"gofmt", "eslint"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		names := make([]string, 0, len(got))
		for _, task := range got {
			names = append(names, task.ToolName)
		}
		sort.Strings(names)
		want := []string{"eslint", "gofmt"}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("filtered tool names = %v, want %v", names, want)
		}
	})

	t.Run("unknown tool yields ToolNotFoundError with sorted available list", func(t *testing.T) {
		_, err := planner.filterTasksBySelectedTools(tasks, []string{"nope", "gofmt"})
		if err == nil {
			t.Fatal("expected error for unknown tool, got nil")
		}
		var nfe *ToolNotFoundError
		if !errors.As(err, &nfe) {
			t.Fatalf("expected *ToolNotFoundError, got %T: %v", err, err)
		}
		if !reflect.DeepEqual(nfe.NotFound, []string{"nope"}) {
			t.Errorf("NotFound = %v, want [nope]", nfe.NotFound)
		}
		want := []string{"eslint", "gofmt", "shellcheck"}
		if !reflect.DeepEqual(nfe.Available, want) {
			t.Errorf("Available = %v, want %v (sorted)", nfe.Available, want)
		}
	})
}

// TestFindFilesByGlobs covers both the cached path (filtering p.cachedFiles)
// and the uncached fallback that scans the filesystem via the traverser.
func TestFindFilesByGlobs(t *testing.T) {
	t.Run("cached path filters cachedFiles by glob", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo",
			cachedFiles: []string{
				"/repo/main.go",
				"/repo/pkg/util.go",
				"/repo/README.md",
				"/repo/web/app.ts",
			},
			cacheInitialized: true,
		}
		got := planner.findFilesByGlobs(context.Background(), []string{"**/*.go"})
		sort.Strings(got)
		want := []string{"/repo/main.go", "/repo/pkg/util.go"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("findFilesByGlobs(cached) = %v, want %v", got, want)
		}
	})

	t.Run("uncached fallback scans the filesystem", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "main.go"), "package main")
		writeFile(t, filepath.Join(root, "doc.md"), "# doc")
		writeFile(t, filepath.Join(root, "pkg", "util.go"), "package pkg")

		planner := &Planner{
			rootPath:         root,
			cwdPath:          root,
			cacheInitialized: false,
		}
		got := planner.findFilesByGlobs(context.Background(), []string{"**/*.go"})
		relPaths := make([]string, 0, len(got))
		for _, f := range got {
			rel, _ := filepath.Rel(root, f)
			relPaths = append(relPaths, rel)
		}
		sort.Strings(relPaths)
		want := []string{"main.go", filepath.Join("pkg", "util.go")}
		if !reflect.DeepEqual(relPaths, want) {
			t.Errorf("findFilesByGlobs(uncached) = %v, want %v", relPaths, want)
		}
	})
}

// TestCreatePerProjectTasksWithFiles covers the per-project task fan-out:
// projectTypes filtering, grouping files to their nearest project, and the
// no-files (run-once-per-project) branch.
func TestCreatePerProjectTasksWithFiles(t *testing.T) {
	projects := []project.ProjectLocation{
		{Path: "/repo", Type: "go"},
		{Path: "/repo/web", Type: "node"},
		{Path: "/repo/api", Type: "go"},
	}

	t.Run("filters projects by tool projectTypes and groups files", func(t *testing.T) {
		planner := &Planner{
			rootPath:         "/repo",
			cwdPath:          "/repo",
			cachedProjects:   projects,
			cacheInitialized: true,
		}
		base := Task{
			ToolName: "eslint",
			Tool:     config.Tool{Name: "eslint", ProjectTypes: []string{"node"}},
		}
		// Both files live under the node project, so the projectTypes filter
		// keeps a single /repo/web task with both files grouped to it.
		files := []string{"/repo/web/app.ts", "/repo/web/util.ts"}
		tasks := planner.createPerProjectTasksWithFiles(context.Background(), base, files)
		if len(tasks) != 1 {
			t.Fatalf("expected 1 task (node project only), got %d: %+v", len(tasks), tasks)
		}
		if tasks[0].ProjectPath != "/repo/web" {
			t.Errorf("ProjectPath = %q, want /repo/web", tasks[0].ProjectPath)
		}
		sort.Strings(tasks[0].Files)
		if !reflect.DeepEqual(tasks[0].Files, []string{"/repo/web/app.ts", "/repo/web/util.ts"}) {
			t.Errorf("Files = %v, want both web files", tasks[0].Files)
		}
	})

	t.Run("no files runs once per matching project", func(t *testing.T) {
		planner := &Planner{
			rootPath:         "/repo",
			cwdPath:          "/repo",
			cachedProjects:   projects,
			cacheInitialized: true,
		}
		base := Task{
			ToolName: "golangci",
			Tool:     config.Tool{Name: "golangci", ProjectTypes: []string{"go"}},
		}
		tasks := planner.createPerProjectTasksWithFiles(context.Background(), base, nil)
		paths := make([]string, 0, len(tasks))
		for _, task := range tasks {
			paths = append(paths, task.ProjectPath)
		}
		sort.Strings(paths)
		want := []string{"/repo", "/repo/api"}
		if !reflect.DeepEqual(paths, want) {
			t.Errorf("project paths = %v, want %v", paths, want)
		}
	})

	t.Run("no projectTypes restriction uses all locations", func(t *testing.T) {
		planner := &Planner{
			rootPath:         "/repo",
			cwdPath:          "/repo",
			cachedProjects:   projects,
			cacheInitialized: true,
		}
		base := Task{ToolName: "any", Tool: config.Tool{Name: "any"}}
		tasks := planner.createPerProjectTasksWithFiles(context.Background(), base, nil)
		if len(tasks) != 3 {
			t.Fatalf("expected 3 tasks (all projects), got %d", len(tasks))
		}
	})
}

// TestFormatParallelizationInfo covers the three rendering branches: a single
// sequential task, all-parallel (with and without exceeding the worker pool),
// and multiple parallel groups.
func TestFormatParallelizationInfo(t *testing.T) {
	f := NewDetailedFormatter()

	mkTasks := func(n int) []Task {
		tasks := make([]Task, n)
		for i := range tasks {
			tasks[i] = Task{ToolName: "tool"}
		}
		return tasks
	}

	t.Run("single task is sequential", func(t *testing.T) {
		got := f.formatParallelizationInfo(mkTasks(1), [][]Task{mkTasks(1)})
		if !strings.Contains(got, "Single task (sequential)") {
			t.Errorf("got %q, want single-task message", got)
		}
	})

	t.Run("all tasks parallel, within worker pool", func(t *testing.T) {
		t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "8")
		tasks := mkTasks(3)
		got := f.formatParallelizationInfo(tasks, [][]Task{tasks})
		if !strings.Contains(got, "All 3 tasks can run in parallel") {
			t.Errorf("got %q, want all-parallel message", got)
		}
		if strings.Contains(got, "Worker Pool Limit") {
			t.Errorf("got %q, did not expect worker-pool note under the limit", got)
		}
	})

	t.Run("all tasks parallel, exceeding worker pool", func(t *testing.T) {
		t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "2")
		tasks := mkTasks(5)
		got := f.formatParallelizationInfo(tasks, [][]Task{tasks})
		if !strings.Contains(got, "Worker Pool Limit: max 2 concurrent workers") {
			t.Errorf("got %q, want worker-pool limit note", got)
		}
	})

	t.Run("multiple parallel groups", func(t *testing.T) {
		tasks := mkTasks(4)
		groups := [][]Task{mkTasks(2), mkTasks(2)}
		got := f.formatParallelizationInfo(tasks, groups)
		if !strings.Contains(got, "2 parallel groups") {
			t.Errorf("got %q, want multi-group message", got)
		}
	})
}

// TestExecuteBatchDryRun drives executeBatch through its branches in dry-run
// mode (no real command execution): whole-project mode, a single chunk, and
// the multi-chunk parallel fan-out.
func TestExecuteBatchDryRun(t *testing.T) {
	e := NewExecutor("/repo", true /*dryRun*/, false, nil, nil)
	cmdInfo := &binmanager.CommandInfo{Type: "binary", Command: "tool"}

	t.Run("whole-project mode (no files) executes once", func(t *testing.T) {
		task := Task{
			ToolName: "gofmt",
			OpConfig: config.ToolOperation{Scope: config.ToolScopeRepository, Args: []string{"-l"}},
		}
		res := e.executeBatch(context.Background(), task, cmdInfo, "/repo", time.Now())
		if !res.Success {
			t.Errorf("expected success in dry-run whole-project mode, got %+v", res)
		}
		if !strings.Contains(res.Output, "[DRY-RUN]") {
			t.Errorf("expected dry-run output, got %q", res.Output)
		}
	})

	t.Run("single chunk executes sequentially", func(t *testing.T) {
		task := Task{
			ToolName: "eslint",
			Files:    []string{"/repo/a.js", "/repo/b.js"},
			OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"--fix", "{files}"}},
		}
		res := e.executeBatch(context.Background(), task, cmdInfo, "/repo", time.Now())
		if !res.Success {
			t.Errorf("expected success for single-chunk batch, got %+v", res)
		}
		if !res.Batch {
			t.Errorf("expected Batch=true")
		}
	})

	t.Run("multiple chunks fan out in parallel", func(t *testing.T) {
		// A tiny command-length limit forces one file per chunk.
		t.Setenv("DATAMITSU_MAX_CMD_LENGTH", "20")
		task := Task{
			ToolName: "prettier",
			Files:    []string{"/repo/long-name-file-1.ts", "/repo/long-name-file-2.ts", "/repo/long-name-file-3.ts"},
			OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"--write", "{files}"}},
		}
		res := e.executeBatch(context.Background(), task, cmdInfo, "/repo", time.Now())
		if !res.Success {
			t.Errorf("expected success for multi-chunk batch, got %+v", res)
		}
		if !res.Batch {
			t.Errorf("expected Batch=true")
		}
	})
}

// TestExecuteBatchChunksParallelCancelled pins that a pre-cancelled context
// short-circuits every chunk and reports the run as cancelled, not a genuine
// tool failure.
func TestExecuteBatchChunksParallelCancelled(t *testing.T) {
	e := NewExecutor("/repo", true, false, nil, nil)
	cmdInfo := &binmanager.CommandInfo{Type: "binary", Command: "tool"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before execution

	task := Task{
		ToolName: "prettier",
		OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"--write", "{files}"}},
	}
	chunks := [][]string{{"a.ts"}, {"b.ts"}, {"c.ts"}}
	res := e.executeBatchChunksParallel(ctx, task, cmdInfo, "/repo", chunks, time.Now())

	if res.Success {
		t.Errorf("expected failure under cancelled context, got success")
	}
	if !res.Cancelled || res.FailureReason != FailureReasonCancelled {
		t.Errorf("expected cancelled classification, got Cancelled=%v reason=%v", res.Cancelled, res.FailureReason)
	}
}

// writeFile is a small helper that creates parent dirs and writes a file.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
