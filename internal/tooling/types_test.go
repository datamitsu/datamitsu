package tooling

import (
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestExecutionPlanGetAppNames(t *testing.T) {
	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{Tasks: []Task{
				{OpConfig: config.ToolOperation{App: "eslint"}},
				{OpConfig: config.ToolOperation{App: "prettier"}},
				{OpConfig: config.ToolOperation{App: "eslint"}}, // duplicate
			}},
			{Tasks: []Task{
				{OpConfig: config.ToolOperation{App: "golangci-lint"}},
				{OpConfig: config.ToolOperation{App: ""}}, // empty skipped
			}},
		},
	}

	got := plan.GetAppNames()
	want := []string{"eslint", "golangci-lint", "prettier"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetAppNames() = %v, want %v", got, want)
	}
}

func TestExecutionPlanGetAppNamesEmpty(t *testing.T) {
	plan := &ExecutionPlan{}
	if got := plan.GetAppNames(); got != nil {
		t.Errorf("GetAppNames() = %v, want nil", got)
	}
}
