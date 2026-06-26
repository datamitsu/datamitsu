package lsp

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestScopeTasksToFile(t *testing.T) {
	origBatch := []string{"fmt"}
	plan := &tooling.ExecutionPlan{
		Groups: []tooling.TaskGroup{{
			Tasks: []tooling.Task{
				{ToolName: "golangci-lint-fmt", OpConfig: config.ToolOperation{Args: origBatch}},
				{ToolName: "prettier", OpConfig: config.ToolOperation{Args: []string{"--write", "{file}"}}},
				{ToolName: "batch-files", OpConfig: config.ToolOperation{Args: []string{"--check", "{files}"}}},
			},
		}},
	}

	scopeTasksToFile(plan, "/abs/foo.go")
	tasks := plan.Groups[0].Tasks

	// Batch tool with no placeholder: the target path is appended so it runs on
	// the one file instead of the whole module.
	if got := tasks[0].OpConfig.Args; len(got) != 2 || got[0] != "fmt" || got[1] != "/abs/foo.go" {
		t.Errorf("batch task args = %v, want [fmt /abs/foo.go]", got)
	}
	// {file}/{files} tasks are already scoped by the planner — leave args intact.
	if got := tasks[1].OpConfig.Args; len(got) != 2 || got[1] != "{file}" {
		t.Errorf("{file} task args = %v, want unchanged", got)
	}
	if got := tasks[2].OpConfig.Args; len(got) != 2 || got[1] != "{files}" {
		t.Errorf("{files} task args = %v, want unchanged", got)
	}
	// Every task is pinned to the single file.
	for i, task := range tasks {
		if len(task.Files) != 1 || task.Files[0] != "/abs/foo.go" {
			t.Errorf("task %d Files = %v, want [/abs/foo.go]", i, task.Files)
		}
	}
	// The shared config slice must not be mutated (clone before append).
	if len(origBatch) != 1 || origBatch[0] != "fmt" {
		t.Errorf("original args slice mutated: %v", origBatch)
	}
}
