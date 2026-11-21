package tooling

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	zerovalue_toolenv "github.com/datamitsu/datamitsu/internal/toolenv"
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
			task: Task{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", OpConfig: config.ToolOperation{Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "eslint", Args: []string{"--fix", "."}}},
			want: "eslint --fix .",
		},
		{
			name: "app without args",
			task: Task{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", OpConfig: config.ToolOperation{Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "gofmt"}},
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
		task := Task{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ProjectPath: "/repo/pkg"}
		if got := getWorkingDirForTask(task, "/repo"); got != "/repo/pkg" {
			t.Errorf("getWorkingDirForTask() = %q, want %q", got, "/repo/pkg")
		}
	})
	t.Run("falls back to rootPath when empty", func(t *testing.T) {
		task := Task{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", ToolName: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: ""}
		if got := getWorkingDirForTask(task, "/repo"); got != "/repo" {
			t.Errorf("getWorkingDirForTask() = %q, want %q", got, "/repo")
		}
	})
}

// perFileTask builds a per-file-scoped task on a single file with a glob so
// distinct files don't overlap (HasOverlap short-circuits on per-file + 1 file).
func perFileTask(name, file string) Task {
	return Task{
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		Operation:    "",
		ProjectPath:  "",
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     name,
		Files:        []string{file},
		OpConfig: config.ToolOperation{
			Arity:             "",
			Granularity:       "",
			ExcludeGlobs:      nil,
			Priority:          0,
			Cache:             nil,
			InvalidateOn:      nil,
			Env:               nil,
			InheritEnv:        nil,
			FailOn:            "",
			Input:             "",
			LSP:               nil,
			Output:            "",
			ManagedConfigRefs: nil,
			App:               name,
			Args:              []string{"--check"},
			Scope:             config.ToolScopePerFile,
			Globs:             []string{"**/*.go"},
		},
	}
}

func repoTask(name string) Task {
	return Task{
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		Operation:    "",
		Files:        nil,
		ProjectPath:  "",
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     name,
		OpConfig: config.ToolOperation{
			Args:              nil,
			Arity:             "",
			Granularity:       "",
			Globs:             nil,
			ExcludeGlobs:      nil,
			Priority:          0,
			Cache:             nil,
			InvalidateOn:      nil,
			Env:               nil,
			InheritEnv:        nil,
			FailOn:            "",
			Input:             "",
			LSP:               nil,
			Output:            "",
			ManagedConfigRefs: nil,
			App:               name,
			Scope:             config.ToolScopeRepository,
		},
	}
}

func TestSummaryFormatter_EmptyPlan(t *testing.T) {
	out := NewSummaryFormatter().Format(&ExecutionPlan{ConfigName: "", FailOn: "", Groups: nil, Skipped: nil}, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "No applicable tools found") {
		t.Errorf("empty plan summary missing notice: %q", out)
	}
}

func TestSummaryFormatter_SingleTask(t *testing.T) {
	plan := &ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []TaskGroup{{Priority: 10, Tasks: []Task{repoTask("golangci-lint")}}}}
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
	plan := &ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []TaskGroup{{
			Priority: 0, Tasks: []Task{
				perFileTask("a", "/repo/x.go"),
				perFileTask("b", "/repo/y.go"),
			},
		}},
	}
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
	plan := &ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []TaskGroup{{
			Priority: 0, Tasks: []Task{
				repoTask("a"),
				repoTask("b"),
			},
		}},
	}
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
	plan := &ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []TaskGroup{{Priority: 0, Tasks: tasks}}}
	out := NewSummaryFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "Worker Pool Limit") {
		t.Errorf("expected worker-pool-limit line with %d tasks, got:\n%s", len(tasks), out)
	}
}

func TestDetailedFormatter_FileLists(t *testing.T) {
	plan := &ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []TaskGroup{{
			Priority: 0, Tasks: []Task{
				{
					perFileCache: "",
					inherited:    zerovalue_toolenv.Inherited{},
					ID:           "",
					Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
					Operation:    "",
					ProjectPath:  "",
					UnitDir:      "",
					UnitMembers:  nil,
					UnitGuards:   nil,
					Coverage:     "",
					ToolName:     "gofmt",
					Files:        []string{"/repo/pkg/a.go", "/repo/pkg/b.go"},
					OpConfig:     config.ToolOperation{Args: nil, Arity: "", Granularity: "", ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "gofmt", Scope: config.ToolScopePerFile, Globs: []string{"**/*.go"}},
				},
			},
		}},
	}
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
	empty := NewDetailedFormatter().Format(&ExecutionPlan{ConfigName: "", FailOn: "", Groups: nil, Skipped: nil}, "/repo", "/repo", config.OpLint)
	if !strings.Contains(empty, "No applicable tools found") {
		t.Errorf("empty detailed plan missing notice: %q", empty)
	}

	plan := &ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []TaskGroup{{Priority: 0, Tasks: []Task{repoTask("golangci-lint")}}}}
	out := NewDetailedFormatter().Format(plan, "/repo", "/repo", config.OpLint)
	if !strings.Contains(out, "Files: whole project") {
		t.Errorf("expected whole-project line, got:\n%s", out)
	}
}

// TestFormatters_EchoTheEffectiveFailOn: every formatter reports the threshold
// a task runs with — its operation's failOn raised by the run's --fail-on — and
// the human ones only when it is not the default.
func TestFormatters_EchoTheEffectiveFailOn(t *testing.T) {
	task := func(name string, failOn config.Severity) Task {
		return Task{perFileCache: "", inherited: zerovalue_toolenv.Inherited{}, ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: name, OpConfig: config.ToolOperation{Args: nil, Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: name, Scope: config.ToolScopeRepository, FailOn: failOn}}
	}
	plan := func(global config.Severity) *ExecutionPlan {
		return &ExecutionPlan{
			ConfigName: "",
			Skipped:    nil, FailOn: global, Groups: []TaskGroup{{Priority: 1, Tasks: []Task{
				task("plain", ""), task("strict", config.SeverityWarning), task("hinted", config.SeverityHint),
			}}},
		}
	}
	cases := []struct {
		global config.Severity
		want   map[string]config.Severity
	}{
		{"", map[string]config.Severity{"plain": "error", "strict": "warning", "hinted": "hint"}},
		{config.SeverityInfo, map[string]config.Severity{"plain": "info", "strict": "info", "hinted": "hint"}},
	}
	for _, c := range cases {
		var parsed PlanJSON
		if err := json.Unmarshal([]byte(NewJSONFormatter().Format(plan(c.global), "/repo", "/repo", config.OpLint)), &parsed); err != nil {
			t.Fatal(err)
		}
		for _, pg := range parsed.Groups[0].ParallelGroups {
			for _, got := range pg.Tasks {
				if want := c.want[got.ToolName]; got.FailOn != string(want) {
					t.Errorf("global %q: %s failOn = %q, want %q", c.global, got.ToolName, got.FailOn, want)
				}
			}
		}
		for _, f := range []PlanFormatter{NewSummaryFormatter(), NewDetailedFormatter()} {
			out := f.Format(plan(c.global), "/repo", "/repo", config.OpLint)
			shown := strings.Count(out, "Fail on: ")
			wantShown := 0
			for _, s := range c.want {
				if s != config.DefaultFailOn {
					wantShown++
				}
			}
			if shown != wantShown {
				t.Errorf("global %q: %T shows %d thresholds, want %d:\n%s", c.global, f, shown, wantShown, out)
			}
		}
	}
}

func TestJSONFormatter_ReportsGranularityArityCoverage(t *testing.T) {
	plan := &ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []TaskGroup{{Priority: 5, Tasks: []Task{
			{
				perFileCache: "",
				inherited:    zerovalue_toolenv.Inherited{},
				ID:           "",
				Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
				Operation:    "",
				ProjectPath:  "",
				UnitDir:      "",
				UnitMembers:  nil,
				UnitGuards:   nil,
				ToolName:     "oxfmt", Files: []string{"/repo/a.json"}, Coverage: CoverageComplete,
				OpConfig: config.ToolOperation{Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "oxfmt", Scope: config.ToolScopeRepository, Args: []string{"{files}"}},
			},
			{
				perFileCache: "",
				inherited:    zerovalue_toolenv.Inherited{},
				ID:           "",
				Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
				Operation:    "",
				Files:        nil,
				ProjectPath:  "",
				UnitDir:      "",
				UnitMembers:  nil,
				UnitGuards:   nil,
				ToolName:     "tsc", Coverage: CoveragePartial,
				OpConfig: config.ToolOperation{Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "tsc", Scope: config.ToolScopePerProject, Args: []string{"--noEmit"}},
			},
		}}},
	}

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
