package tooling

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// TestStepCallback: every parallel group that ran is reported once, in the
// order it ran, after its results — the group a failure stopped the run in
// too — and a group the run never reached is not.
func TestStepCallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"fails":   shellApp("exit 1"),
		"passes":  shellApp("exit 0"),
		"overlap": shellApp("exit 0"),
		"later":   shellApp("exit 0"),
	}}
	plan := &ExecutionPlan{Groups: []TaskGroup{
		{Priority: 10, Tasks: []Task{
			lintTask(t, "fails", config.ToolScopePerProject, filepath.Join(root, "a")),
			lintTask(t, "passes", config.ToolScopePerProject, filepath.Join(root, "b")),
			lintTask(t, "overlap", config.ToolScopeRepository, ""),
		}},
		{Priority: 20, Tasks: []Task{lintTask(t, "later", config.ToolScopeRepository, "")}},
	}}
	tests := []struct {
		failFast bool
		want     []string
	}{
		{failFast: false, want: []string{"1:fails,passes", "2:overlap", "3:later"}},
		{failFast: true, want: []string{"1:fails,passes"}},
	}
	for _, tt := range tests {
		e := NewExecutor(root, false, tt.failFast, appManager, nil)
		var got []string
		seen := map[string]bool{}
		e.SetResultCallback(func(r ExecutionResult) { seen[r.ToolName] = true })
		e.SetStepCallback(func(step int, tasks []Task) {
			names := make([]string, 0, len(tasks))
			for _, task := range tasks {
				if !seen[task.ToolName] {
					t.Errorf("step %d reported before the result of %s", step, task.ToolName)
				}
				names = append(names, task.ToolName)
			}
			sort.Strings(names)
			got = append(got, fmt.Sprintf("%d:%s", step, strings.Join(names, ",")))
		})
		_, _ = e.Execute(context.Background(), plan)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("failFast=%v: steps = %v, want %v", tt.failFast, got, tt.want)
		}
	}
}
