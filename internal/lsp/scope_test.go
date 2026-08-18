package lsp

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func planWith(ops ...config.ToolOperation) *tooling.ExecutionPlan {
	tasks := make([]tooling.Task, 0, len(ops))
	for i, op := range ops {
		tasks = append(tasks, tooling.Task{
			ToolName: string(rune('a' + i)),
			OpConfig: op,
			Files:    []string{"/repo/a.ts"},
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

func TestFilterPlanForEditor(t *testing.T) {
	t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "unit")

	plan := planWith(
		// file granularity: cheap, always runs
		config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"{file}"}},
		// unit granularity: allowed by the default policy
		config.ToolOperation{Scope: config.ToolScopePerProject, Args: []string{"run"}},
		// repo granularity: never on save
		config.ToolOperation{Scope: config.ToolScopeRepository, Args: []string{"run"}},
	)
	filterPlanForEditor(plan)

	got := toolNames(plan)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("kept %v, want the file and unit tasks only", got)
	}
}

func TestFilterPlanForEditorTargetPolicy(t *testing.T) {
	t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "target")

	plan := planWith(
		config.ToolOperation{Scope: config.ToolScopePerFile, Args: []string{"{file}"}},
		config.ToolOperation{Scope: config.ToolScopePerProject, Args: []string{"run"}},
	)
	filterPlanForEditor(plan)

	if got := toolNames(plan); len(got) != 1 || got[0] != "a" {
		t.Errorf("kept %v, want only the file-granularity task", got)
	}
}

// The default must not silently disable format-on-save for Go, whose fix
// operations are per-project with no file arguments.
func TestFilterPlanForEditorDefaultKeepsUnitTasks(t *testing.T) {
	t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "")

	plan := planWith(config.ToolOperation{
		Scope: config.ToolScopePerProject,
		Args:  []string{"run", "--fix", "--allow-parallel-runners"},
	})
	filterPlanForEditor(plan)

	if got := toolNames(plan); len(got) != 1 {
		t.Errorf("kept %v, want the unit task under the default policy", got)
	}
}

// A repository-wide fix on every save is never acceptable, whatever the policy.
func TestFilterPlanForEditorNeverRunsRepoTasks(t *testing.T) {
	t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", "repo")

	plan := planWith(config.ToolOperation{Scope: config.ToolScopeRepository, Args: []string{"run"}})
	filterPlanForEditor(plan)

	if got := toolNames(plan); len(got) != 0 {
		t.Errorf("kept %v, want nothing", got)
	}
}
