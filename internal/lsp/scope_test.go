package lsp

import (
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func planWith(ops ...config.ToolOperation) *tooling.ExecutionPlan {
	tasks := make([]tooling.Task, 0, len(ops))
	for i, op := range ops {
		tasks = append(tasks, tooling.Task{
			ToolName:    string(rune('a' + i)),
			OpConfig:    op,
			ProjectPath: "/repo/pkg",
			Files:       []string{"/repo/pkg/a.ts"},
		})
	}
	return &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: tasks}}}
}

func toolNames(plan *tooling.ExecutionPlan) []string {
	var out []string
	for _, g := range plan.Groups {
		for _, task := range g.Tasks {
			out = append(out, task.ToolName)
		}
	}
	return out
}

var (
	fileOp = config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"{file}"}}
	unitOp = config.ToolOperation{Scope: config.ToolScopePerProject, Args: []string{"fmt"}, Globs: []string{"**/*.go"}}
	// noGlobsOp claims every file in its unit, like `golangci-lint fmt`.
	noGlobsOp = config.ToolOperation{Scope: config.ToolScopePerProject, Args: []string{"fmt"}}
	repoOp    = config.ToolOperation{Scope: config.ToolScopeRepository, Args: []string{"run"}}
)

func withLSP(op config.ToolOperation, on bool) config.ToolOperation {
	op.LSP = &on
	return op
}

// Any false wins and a true needs both sides: the author's lsp: false is an
// absolute veto, the user's format.tools can disable anything and lift a unit
// task over a "target" session, but nothing reaches past the project's policy
// or runs a repository-wide fix on save.
func TestEditorDecision(t *testing.T) {
	const saved = "/repo/pkg/a.go"

	tests := []struct {
		name       string
		op         config.ToolOperation
		unit       string // task.ProjectPath
		project    config.WidenTo
		session    config.WidenTo
		tools      map[string]bool
		wantRun    bool
		wantReason string
	}{
		{name: "file runs", op: fileOp, unit: "/repo/pkg", project: config.WidenToUnit, session: config.WidenToTarget, wantRun: true},
		{name: "lsp true is the same as unset", op: withLSP(fileOp, true), unit: "/repo/pkg", project: config.WidenToUnit, session: config.WidenToUnit, wantRun: true},
		{
			name: "lsp false drops a file task", op: withLSP(fileOp, false), unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToUnit, wantReason: reasonLSPFalse,
		},
		{
			name: "lsp false beats tools true", op: withLSP(unitOp, false), unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToUnit, tools: map[string]bool{"t": true}, wantReason: reasonLSPFalse,
		},
		{
			name: "tools false drops a file task", op: fileOp, unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToUnit, tools: map[string]bool{"t": false}, wantReason: reasonToolsFalse,
		},
		{
			name: "tools false drops a unit task", op: unitOp, unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToUnit, tools: map[string]bool{"t": false}, wantReason: reasonToolsFalse,
		},
		{
			name: "repo never runs", op: repoOp, unit: "/repo",
			project: config.WidenToRepo, session: config.WidenToUnit, wantReason: reasonRepo,
		},
		{
			name: "tools true does not lift a repo task", op: repoOp, unit: "/repo",
			project: config.WidenToRepo, session: config.WidenToUnit, tools: map[string]bool{"t": true}, wantReason: reasonRepo,
		},
		{name: "unit under unit session", op: unitOp, unit: "/repo/pkg", project: config.WidenToUnit, session: config.WidenToUnit, wantRun: true},
		{name: "unit under a repo project is still min(project, session)", op: unitOp, unit: "/repo/pkg", project: config.WidenToRepo, session: config.WidenToUnit, wantRun: true},
		{name: "unset project policy takes the default", op: unitOp, unit: "/repo/pkg", project: "", session: config.WidenToUnit, wantRun: true},
		{
			name: "unit under a target session", op: unitOp, unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToTarget, wantReason: reasonSessionTarget,
		},
		{
			name: "tools true lifts a unit task over a target session", op: unitOp, unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToTarget, tools: map[string]bool{"t": true}, wantRun: true,
		},
		{
			name: "unit under a target project", op: unitOp, unit: "/repo/pkg",
			project: config.WidenToTarget, session: config.WidenToUnit, wantReason: reasonProjectTarget,
		},
		{
			name: "tools true never passes a target project", op: unitOp, unit: "/repo/pkg",
			project: config.WidenToTarget, session: config.WidenToTarget, tools: map[string]bool{"t": true}, wantReason: reasonProjectTarget,
		},
		{
			name: "both target names the project", op: unitOp, unit: "/repo/pkg",
			project: config.WidenToTarget, session: config.WidenToTarget, wantReason: reasonProjectTarget,
		},
		{
			name: "glob-less unit task stays out", op: noGlobsOp, unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToUnit, wantReason: reasonNoGlobs,
		},
		{
			name: "tools true opts a glob-less unit task in", op: noGlobsOp, unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToTarget, tools: map[string]bool{"t": true}, wantRun: true,
		},
		{
			name: "an opted-in glob-less task is still bounded by the project", op: noGlobsOp, unit: "/repo/pkg",
			project: config.WidenToTarget, session: config.WidenToUnit, tools: map[string]bool{"t": true}, wantReason: reasonProjectTarget,
		},
		{
			name: "lsp false beats opting a glob-less task in", op: withLSP(noGlobsOp, false), unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToUnit, tools: map[string]bool{"t": true}, wantReason: reasonLSPFalse,
		},
		{
			name: "tools false on a glob-less task names the user's choice", op: noGlobsOp, unit: "/repo/pkg",
			project: config.WidenToUnit, session: config.WidenToUnit, tools: map[string]bool{"t": false}, wantReason: reasonToolsFalse,
		},
		{
			name: "glob-less file task is unaffected", op: config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"{file}"}},
			unit: "/repo/pkg", project: config.WidenToUnit, session: config.WidenToUnit, wantRun: true,
		},
		{
			name: "another module is dropped silently", op: unitOp, unit: "/repo/other",
			project: config.WidenToUnit, session: config.WidenToUnit,
		},
		{
			name: "another module stays silent whatever else applies", op: withLSP(unitOp, false), unit: "/repo/other",
			project: config.WidenToUnit, session: config.WidenToUnit, tools: map[string]bool{"t": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := tooling.Task{ToolName: "t", OpConfig: tt.op, ProjectPath: tt.unit}
			run, reason := editorDecision(task, saved, tt.project, sessionPolicy(tt.session, tt.tools))
			if run != tt.wantRun || reason != tt.wantReason {
				t.Errorf("editorDecision() = (%v, %q), want (%v, %q)", run, reason, tt.wantRun, tt.wantReason)
			}
		})
	}
}

func TestFilterPlanForEditor(t *testing.T) {
	plan := planWith(
		fileOp, // file granularity: cheap, always runs
		unitOp, // unit granularity: allowed by the default policy
		repoOp, // repo granularity: never on save
	)
	left := filterPlanForEditor(plan, "/repo/pkg/a.ts", "", sessionPolicy(config.WidenToUnit, nil))

	got := toolNames(plan)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("kept %v, want the file and unit tasks only", got)
	}
	if want := []leftOut{{Tool: "c", Reason: reasonRepo}}; !slices.Equal(left, want) {
		t.Errorf("left out %v, want %v", left, want)
	}
}

func TestFilterPlanForEditorTargetPolicy(t *testing.T) {
	plan := planWith(fileOp, unitOp)
	left := filterPlanForEditor(plan, "/repo/pkg/a.ts", "", sessionPolicy(config.WidenToTarget, nil))

	if got := toolNames(plan); len(got) != 1 || got[0] != "a" {
		t.Errorf("kept %v, want only the file-granularity task", got)
	}
	if want := []leftOut{{Tool: "b", Reason: reasonSessionTarget}}; !slices.Equal(left, want) {
		t.Errorf("left out %v, want %v", left, want)
	}
}

// The default must not silently disable format-on-save for Go, whose fix
// operations are per-project with no file arguments.
func TestFilterPlanForEditorDefaultKeepsUnitTasks(t *testing.T) {
	t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "")

	plan := planWith(config.ToolOperation{
		Scope: config.ToolScopePerProject,
		Args:  []string{"run", "--fix", "--allow-parallel-runners"},
		Globs: []string{"**/*.go"},
	})
	filterPlanForEditor(plan, "/repo/pkg/a.ts", "", envFormatPolicy())

	if got := toolNames(plan); len(got) != 1 {
		t.Errorf("kept %v, want the unit task under the default policy", got)
	}
}

// A unit operation with no globs is planned once per project regardless of what
// was saved. Without a containment check, saving one file ran it in every
// module — and these tools fix in place, so files the editor never opened were
// rewritten behind its back. Those modules are not reported either: they are
// not what the user saved.
func TestFilterPlanForEditorSkipsOtherUnits(t *testing.T) {
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{
		{ToolName: "mine", OpConfig: unitOp, ProjectPath: "/repo/svc/a"},
		{ToolName: "theirs", OpConfig: unitOp, ProjectPath: "/repo/svc/b"},
	}}}}

	left := filterPlanForEditor(plan, "/repo/svc/a/main.go", "", sessionPolicy(config.WidenToUnit, nil))

	if got := toolNames(plan); len(got) != 1 || got[0] != "mine" {
		t.Errorf("kept %v, want only the unit holding the saved file", got)
	}
	if len(left) != 0 {
		t.Errorf("left out %v, want other modules unreported", left)
	}
}

// The watchdog counts groups, so a group the policy emptied must not count as
// one that ran.
func TestFilterPlanForEditorDropsEmptyGroups(t *testing.T) {
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{
		{Priority: 0, Tasks: []tooling.Task{{ToolName: "repo-only", OpConfig: repoOp, ProjectPath: "/repo"}}},
		{Priority: 1, Tasks: []tooling.Task{{ToolName: "fmt", OpConfig: fileOp, ProjectPath: "/repo/pkg"}}},
	}}

	filterPlanForEditor(plan, "/repo/pkg/a.ts", "", sessionPolicy(config.WidenToUnit, nil))

	if len(plan.Groups) != 1 || plan.Groups[0].Priority != 1 {
		t.Errorf("groups = %+v, want only the priority-1 group", plan.Groups)
	}
}

func TestLeftOutNotice(t *testing.T) {
	if got := leftOutNotice("a.go", nil); got != "" {
		t.Errorf("leftOutNotice(nil) = %q, want empty", got)
	}
	got := leftOutNotice("pkg/a.go", []leftOut{
		{Tool: "golangci-lint", Reason: reasonSessionTarget},
		{Tool: "pre-commit", Reason: reasonRepo},
		{Tool: "golangci-lint", Reason: reasonSessionTarget},
	})
	want := "format pkg/a.go: left out by the editor policy: " +
		"golangci-lint (project-wide, editor policy is target), pre-commit (repository-wide, never on save)"
	if got != want {
		t.Errorf("leftOutNotice() =\n  %s\nwant\n  %s", got, want)
	}
}

// A unit operation without globs is planned for every file in its unit, so
// saving a TypeScript file would run `golangci-lint fmt` over the whole Go
// module and the watchdog would stop before the formatters the file needs. It
// stays out unless the user opts it in, and the notice says why.
func TestFilterPlanForEditorUnitTasksWithoutGlobs(t *testing.T) {
	tests := []struct {
		name     string
		tools    map[string]bool
		wantKept []string
		wantLeft []leftOut
	}{
		{
			name:     "left out and reported",
			wantKept: []string{"a", "b"},
			wantLeft: []leftOut{{Tool: "c", Reason: reasonNoGlobs}},
		},
		{
			name:     "opted in by format.tools",
			tools:    map[string]bool{"c": true},
			wantKept: []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := planWith(fileOp, unitOp, noGlobsOp)
			left := filterPlanForEditor(plan, "/repo/pkg/a.ts", "", sessionPolicy(config.WidenToUnit, tt.tools))

			if got := toolNames(plan); !slices.Equal(got, tt.wantKept) {
				t.Errorf("kept %v, want %v", got, tt.wantKept)
			}
			if !slices.Equal(left, tt.wantLeft) {
				t.Errorf("left out %v, want %v", left, tt.wantLeft)
			}
		})
	}
}
