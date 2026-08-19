package tooling

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
)

func TestMakeRelativePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		baseDir string
		want    string
	}{
		{"under base", "/repo/pkg/a.go", "/repo", "pkg/a.go"},
		{"base itself", "/repo", "/repo", "."},
		{"sibling escapes base", "/other/a.go", "/repo", "../other/a.go"},
		// filepath.Rel cannot relate an absolute base to a relative target →
		// the absolute (here: relative) input is returned unchanged.
		{"unrelatable returns input", "relative/a.go", "/repo", "relative/a.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := makeRelativePath(tt.path, tt.baseDir); got != tt.want {
				t.Errorf("makeRelativePath(%q, %q) = %q, want %q", tt.path, tt.baseDir, got, tt.want)
			}
		})
	}
}

func TestBuildCommandTemplate(t *testing.T) {
	tests := []struct {
		name string
		task Task
		want string
	}{
		{
			name: "app with args",
			task: Task{OpConfig: config.ToolOperation{App: "eslint", Args: []string{"--fix", "."}}},
			want: "eslint --fix .",
		},
		{
			name: "app without args",
			task: Task{OpConfig: config.ToolOperation{App: "gofmt"}},
			want: "gofmt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildCommandTemplate(tt.task); got != tt.want {
				t.Errorf("buildCommandTemplate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetWorkingDirForTask(t *testing.T) {
	t.Run("uses ProjectPath when set", func(t *testing.T) {
		task := Task{ProjectPath: "/repo/pkg"}
		if got := getWorkingDirForTask(task, "/repo"); got != "/repo/pkg" {
			t.Errorf("getWorkingDirForTask() = %q, want %q", got, "/repo/pkg")
		}
	})
	t.Run("falls back to rootPath when empty", func(t *testing.T) {
		task := Task{}
		if got := getWorkingDirForTask(task, "/repo"); got != "/repo" {
			t.Errorf("getWorkingDirForTask() = %q, want %q", got, "/repo")
		}
	})
}

// perFileTask builds a per-file-scoped task on a single file with a glob so
// distinct files don't overlap (HasOverlap short-circuits on per-file + 1 file).
func perFileTask(name, file string) Task {
	return Task{
		ToolName: name,
		Files:    []string{file},
		OpConfig: config.ToolOperation{
			App:   name,
			Args:  []string{"--check"},
			Scope: config.ToolScopePerFile,
			Globs: []string{"**/*.go"},
		},
	}
}

func repoTask(name string) Task {
	return Task{
		ToolName: name,
		OpConfig: config.ToolOperation{
			App:   name,
			Scope: config.ToolScopeRepository,
		},
	}
}

func TestSummaryFormatter_EmptyPlan(t *testing.T) {
	out := NewSummaryFormatter().Format(&ExecutionPlan{}, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "No applicable tools found") {
		t.Errorf("empty plan summary missing notice: %q", out)
	}
}

func TestSummaryFormatter_SingleTask(t *testing.T) {
	plan := &ExecutionPlan{Groups: []TaskGroup{{Priority: 10, Tasks: []Task{repoTask("golangci-lint")}}}}
	out := NewSummaryFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	for _, want := range []string{
		"Execution Plan for 'lint' operation",
		"Priority Group 1 (priority: 10)",
		"golangci-lint",
		"Files: whole project",
		"Single task (sequential)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q in:\n%s", want, out)
		}
	}
}

func TestSummaryFormatter_AllParallel(t *testing.T) {
	// Two per-file tasks on different files → no overlap → one parallel group.
	plan := &ExecutionPlan{Groups: []TaskGroup{{Tasks: []Task{
		perFileTask("a", "/repo/x.go"),
		perFileTask("b", "/repo/y.go"),
	}}}}
	out := NewSummaryFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "All 2 tasks can run in parallel") {
		t.Errorf("expected all-parallel line, got:\n%s", out)
	}
	if !strings.Contains(out, "Files: 1 matched") {
		t.Errorf("expected file-count line, got:\n%s", out)
	}
}

func TestSummaryFormatter_MultipleParallelGroups(t *testing.T) {
	// Two repository-scoped tasks always overlap → two parallel groups.
	plan := &ExecutionPlan{Groups: []TaskGroup{{Tasks: []Task{
		repoTask("a"),
		repoTask("b"),
	}}}}
	out := NewSummaryFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "2 parallel groups") {
		t.Errorf("expected multi-group line, got:\n%s", out)
	}
}

func TestSummaryFormatter_WorkerPoolLimit(t *testing.T) {
	maxWorkers := env.GetMaxParallelWorkers()
	tasks := make([]Task, 0, maxWorkers+1)
	for i := 0; i <= maxWorkers; i++ {
		tasks = append(tasks, perFileTask("t", "/repo/file"+string(rune('a'+i))+".go"))
	}
	plan := &ExecutionPlan{Groups: []TaskGroup{{Tasks: tasks}}}
	out := NewSummaryFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "Worker Pool Limit") {
		t.Errorf("expected worker-pool-limit line with %d tasks, got:\n%s", len(tasks), out)
	}
}

func TestDetailedFormatter_FileLists(t *testing.T) {
	plan := &ExecutionPlan{Groups: []TaskGroup{{Tasks: []Task{
		{
			ToolName: "gofmt",
			Files:    []string{"/repo/pkg/a.go", "/repo/pkg/b.go"},
			OpConfig: config.ToolOperation{App: "gofmt", Scope: config.ToolScopePerFile, Globs: []string{"**/*.go"}},
		},
	}}}}
	out := NewDetailedFormatter().Format(plan, "/repo", "/repo", config.OpFix)
	for _, want := range []string{
		"Files (2):",
		"• pkg/a.go",
		"• pkg/b.go",
		"Single task (sequential)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detailed output missing %q in:\n%s", want, out)
		}
	}
}

func TestDetailedFormatter_EmptyAndWholeProject(t *testing.T) {
	empty := NewDetailedFormatter().Format(&ExecutionPlan{}, "/repo", "/repo", config.OpLint)
	if !strings.Contains(empty, "No applicable tools found") {
		t.Errorf("empty detailed plan missing notice: %q", empty)
	}

	plan := &ExecutionPlan{Groups: []TaskGroup{{Tasks: []Task{repoTask("golangci-lint")}}}}
	out := NewDetailedFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "Files: whole project") {
		t.Errorf("expected whole-project line, got:\n%s", out)
	}
}

func TestJSONFormatter_ReportsGranularityArityCoverage(t *testing.T) {
	plan := &ExecutionPlan{Groups: []TaskGroup{{Priority: 5, Tasks: []Task{
		{
			ToolName: "oxfmt", Files: []string{"/repo/a.json"}, Coverage: CoverageComplete,
			OpConfig: config.ToolOperation{App: "oxfmt", Scope: config.ToolScopeRepository, Args: []string{"{files}"}},
		},
		{
			ToolName: "tsc", Coverage: CoveragePartial,
			OpConfig: config.ToolOperation{App: "tsc", Scope: config.ToolScopePerProject, Args: []string{"--noEmit"}},
		},
	}}}}

	out := NewJSONFormatter().Format(plan, "/repo", "/cwd", config.OpLint)
	var parsed PlanJSON
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	got := map[string][3]string{}
	for _, pg := range parsed.Groups[0].ParallelGroups {
		for _, task := range pg.Tasks {
			got[task.ToolName] = [3]string{task.Granularity, task.Arity, task.Coverage}
		}
	}
	for tool, want := range map[string][3]string{
		"oxfmt": {"file", "many", "complete"},
		"tsc":   {"unit", "none", "partial"},
	} {
		if got[tool] != want {
			t.Errorf("%s: granularity/arity/coverage = %v, want %v", tool, got[tool], want)
		}
	}
}
