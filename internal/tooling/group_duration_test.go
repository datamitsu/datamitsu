package tooling

import (
	"context"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// TestExecuteGroupRecordsDurationOnFailFast pins that a group which exits early
// still reports how long it took.
//
// The two fail-fast branches used to `return result` before the assignment at
// the bottom of executeGroup, so a failing run reported "done in 0ms" for work
// that had really taken seconds — and with several groups, the footer showed the
// sum of only the groups that happened not to fail. That is invisible in a
// passing run, which is why this test forces a failure.
func TestExecuteGroupRecordsDurationOnFailFast(t *testing.T) {
	tests := []struct {
		name     string
		tasks    []Task
		failFast bool
	}{
		{
			name:     "single failing task with fail-fast",
			tasks:    []Task{sleepTask(t, "alpha", true)},
			failFast: true,
		},
		{
			name: "parallel failing tasks with fail-fast",
			tasks: []Task{
				sleepTask(t, "alpha", true),
				sleepTask(t, "bravo", true),
			},
			failFast: true,
		},
		{
			name:     "passing task",
			tasks:    []Task{sleepTask(t, "alpha", false)},
			failFast: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewExecutor(t.TempDir(), false, tt.failFast,
				&mockAppManager{binaries: map[string]string{"sh": "/bin/sh"}}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			got := e.executeGroup(ctx, TaskGroup{Priority: 1, Tasks: tt.tasks}, cancel)

			if got.WallClockDuration < 0 {
				t.Fatalf("WallClockDuration = %d, want >= 0", got.WallClockDuration)
			}
			// The tasks sleep, so a correctly recorded duration cannot be zero.
			if got.WallClockDuration == 0 {
				t.Errorf("WallClockDuration = 0 for a group that ran a sleeping task; "+
					"the early return skipped the measurement (success=%v)", got.Success)
			}
		})
	}
}

// sleepTask builds a task that runs `sleep`, exiting non-zero when fail is set,
// so the group takes measurable time either way.
func sleepTask(t *testing.T, tool string, fail bool) Task {
	t.Helper()
	script := "sleep 0.05"
	if fail {
		script = "sleep 0.05; exit 3"
	}
	return Task{
		ToolName: tool,
		Tool:     config.Tool{Name: tool},
		OpConfig: config.ToolOperation{
			App:   "sh",
			Args:  []string{"-c", script},
			Scope: config.ToolScopeRepository,
		},
		Operation:   config.OpLint,
		ProjectPath: t.TempDir(),
	}
}
