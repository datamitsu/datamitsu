package lsp

import (
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// The session policy is ambient — it applies to every save — so it may narrow
// what runs but never widen it. "repo" is not a legal editor policy at all: a
// repository-wide fix on save is never acceptable.
func TestEditorWidenTo(t *testing.T) {
	tests := []struct {
		set  string
		want config.WidenTo
	}{
		{"target", config.WidenToTarget},
		{"unit", config.WidenToUnit},
		{"repo", config.WidenToUnit},
		{"", config.WidenToUnit},
		{"nonsense", config.WidenToUnit},
	}

	for _, tt := range tests {
		t.Run("policy="+tt.set, func(t *testing.T) {
			t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", tt.set)
			if got := editorWidenTo(); got != tt.want {
				t.Errorf("editorWidenTo() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The session policy is ambient, so it is clamped to the project's. A project
// that declared fix: "target" used to get the editor default of "unit" anyway,
// and saving one file ran an in-place, unit-granularity formatter over the whole
// project — precisely the blast radius that setting exists to prevent.
func TestFilterPlanForEditorClampsToTheProjectPolicy(t *testing.T) {
	t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "unit")

	unitOp := config.ToolOperation{Scope: config.ToolScopePerProject, Args: []string{"fmt"}}
	fileOp := config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"{file}"}}
	newPlan := func() *tooling.ExecutionPlan {
		return &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{
			{ToolName: "gofmt", OpConfig: unitOp, ProjectPath: "/repo/pkg"},
			{ToolName: "shfmt", OpConfig: fileOp, ProjectPath: "/repo/pkg"},
		}}}}
	}

	t.Run("project target overrides the looser session default", func(t *testing.T) {
		plan := newPlan()
		filterPlanForEditor(plan, "/repo/pkg/a.go", config.WidenToTarget)

		got := toolNames(plan)
		if len(got) != 1 || got[0] != "shfmt" {
			t.Errorf("kept %v, want only the file-granularity task", got)
		}
	})

	t.Run("a looser project policy does not widen the session", func(t *testing.T) {
		t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "target")
		plan := newPlan()
		filterPlanForEditor(plan, "/repo/pkg/a.go", config.WidenToRepo)

		got := toolNames(plan)
		if len(got) != 1 || got[0] != "shfmt" {
			t.Errorf("kept %v; the narrower of the two must win", got)
		}
	})

	t.Run("agreeing policies keep the unit task", func(t *testing.T) {
		plan := newPlan()
		filterPlanForEditor(plan, "/repo/pkg/a.go", config.WidenToUnit)

		if got := toolNames(plan); len(got) != 2 {
			t.Errorf("kept %v, want both tasks", got)
		}
	})
}

// A unit operation with no globs is planned once per project regardless of what
// was saved, so without a containment check saving one file runs it in every
// module — and these tools fix in place, rewriting files the editor never opened.
func TestTaskCoversPath(t *testing.T) {
	unit := filepath.FromSlash("/repo/svc/a")

	tests := []struct {
		name string
		task tooling.Task
		path string
		want bool
	}{
		{
			"file inside the unit",
			tooling.Task{ProjectPath: unit},
			filepath.Join(unit, "main.go"), true,
		},
		{
			"file in a nested directory",
			tooling.Task{ProjectPath: unit},
			filepath.Join(unit, "internal", "x.go"), true,
		},
		{
			"file in a sibling unit",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/svc/b/main.go"), false,
		},
		{
			// "/repo/svc/ab" must not count as inside "/repo/svc/a".
			"sibling with a shared name prefix",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/svc/ab/main.go"), false,
		},
		{
			"file above the unit",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/main.go"), false,
		},
		{
			// No project path means the task is not unit-bound.
			"task with no unit covers everything",
			tooling.Task{},
			filepath.FromSlash("/repo/anywhere.go"), true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskCoversPath(tt.task, tt.path); got != tt.want {
				t.Errorf("taskCoversPath(%q, %q) = %v, want %v",
					tt.task.ProjectPath, tt.path, got, tt.want)
			}
		})
	}
}
