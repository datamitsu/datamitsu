package runner

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func task(tool, projectPath string) tooling.Task {
	return tooling.Task{
		ToolName:    tool,
		ProjectPath: projectPath,
		OpConfig:    config.ToolOperation{App: tool, Scope: config.ToolScopeRepository},
	}
}

// relDir mirrors the executor's TaskDir for tasks whose ProjectPath is already
// relative: "" is the root.
func relDir(t tooling.Task) string { return t.ProjectPath }

// The planned tasks without a result are the ones the run never reached. Tasks
// of one tool in one directory are interchangeable, so they are matched by
// count, and the answer keeps plan order.
func TestUnreachedTasks(t *testing.T) {
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{
		{Priority: 10, Tasks: []tooling.Task{task("fmt", ""), task("fmt", ""), task("fmt", ""), task("tsc", "pkg/a")}},
		{Priority: 20, Tasks: []tooling.Task{task("lint", ""), task("tsc", "pkg/b")}},
	}}
	results := []tooling.GroupExecutionResult{{Priority: 10, Results: []tooling.ExecutionResult{
		{ToolName: "fmt", Success: false},
		{ToolName: "fmt", Cancelled: true, FailureReason: tooling.FailureReasonCancelled},
		{ToolName: "tsc", RelativeDir: "pkg/a", Success: true},
	}}}

	got := unreachedTasks(plan, results, relDir, stopFailFast)

	want := []stoppedTask{
		{tool: "fmt", cause: stopFailFast},
		{tool: "lint", cause: stopFailFast},
		{tool: "tsc", dir: "pkg/b", cause: stopFailFast},
	}
	if len(got) != len(want) {
		t.Fatalf("unreachedTasks() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("unreachedTasks()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPartialTasks(t *testing.T) {
	results := []tooling.GroupExecutionResult{
		{Results: []tooling.ExecutionResult{{ToolName: "a", FilesNotRun: 2}, {ToolName: "b"}}},
		{Results: []tooling.ExecutionResult{{ToolName: "c", FilesNotRun: 1}}},
	}
	if got := partialTasks(results); got != 2 {
		t.Errorf("partialTasks() = %d, want 2", got)
	}
}

func TestStoppedFromResult(t *testing.T) {
	started := time.Now()
	tests := []struct {
		name   string
		result tooling.ExecutionResult
		want   stoppedTask
		msg    string
	}{
		{
			name: "killed by fail-fast",
			result: tooling.ExecutionResult{
				ToolName: "tsc", RelativeDir: "pkg/a", Cancelled: true,
				FailureReason: tooling.FailureReasonCancelled, StartedAt: started, Duration: 40,
			},
			want: stoppedTask{tool: "tsc", dir: "pkg/a", started: true, cause: stopFailFast, durationMs: 40},
			msg:  "cancelled: fail-fast",
		},
		{
			name: "waited for a worker",
			result: tooling.ExecutionResult{
				ToolName: "tsc", RelativeDir: "pkg/b", Cancelled: true,
				FailureReason: tooling.FailureReasonCancelled,
			},
			want: stoppedTask{tool: "tsc", dir: "pkg/b", cause: stopFailFast},
			msg:  "not started: fail-fast",
		},
		{
			name: "interrupted",
			result: tooling.ExecutionResult{
				ToolName: "tsc", Cancelled: true,
				FailureReason: tooling.FailureReasonInterrupted, StartedAt: started,
			},
			want: stoppedTask{tool: "tsc", started: true, cause: stopInterrupted},
			msg:  "cancelled: interrupted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stoppedFromResult(tt.result)
			if got != tt.want {
				t.Errorf("stoppedFromResult() = %+v, want %+v", got, tt.want)
			}
			if got.eventMsg() != tt.msg {
				t.Errorf("eventMsg() = %q, want %q", got.eventMsg(), tt.msg)
			}
		})
	}
}

// Stopped tasks of one kind share a line, so a per-file tool never reached over
// many files is one line with a count, not one line per file.
func TestPrintStoppedTasks(t *testing.T) {
	t.Setenv("CI", "true")
	stopped := []stoppedTask{
		{tool: "tsc", dir: "pkg/a", started: true, cause: stopFailFast},
		{tool: "fmt", cause: stopFailFast},
		{tool: "fmt", cause: stopFailFast},
		{tool: "fmt", cause: stopFailFast},
	}
	out := captureStdout(t, func() { printStoppedTasks(stopped, 5) })
	want := "┃ ⊘ tsc [pkg/a]  cancelled (fail-fast)\n┃ ⊘ fmt    not started (fail-fast) ×3\n"
	if out != want {
		t.Errorf("printStoppedTasks() printed\n%q\nwant\n%q", out, want)
	}
}

// A run returns one error: an interruption wins because the run did not
// finish; then a tool failure (exit 1), then --fail-on-skip, then
// --require-coverage.
func TestOutcomePrecedence(t *testing.T) {
	opErr := errors.New("operation failed")
	tests := []struct {
		name        string
		interrupted bool
		opErr       error
		skip        bool
		coverage    bool
		want        string
	}{
		{name: "clean"},
		{name: "interruption wins", interrupted: true, opErr: opErr, skip: true, coverage: true, want: "interrupted by SIGINT"},
		{name: "tool failure wins, the assertions still reported", opErr: opErr, skip: true, coverage: true, want: "operation failed\n--fail-on-skip: 1 tool(s) have no binary for this host: typstyle\n--require-coverage=unit"},
		{name: "skip and coverage both reported", skip: true, coverage: true, want: "--fail-on-skip: 1 tool(s) have no binary for this host: typstyle\n--require-coverage=unit"},
		{name: "skip", skip: true, want: "--fail-on-skip"},
		{name: "coverage", coverage: true, want: "--require-coverage=unit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			if tt.interrupted {
				cancel(interruptedError{sig: syscall.SIGINT})
			}
			sc := &sharedContext{platformSkipped: map[string]struct{}{}, narrowed: map[string]struct{}{}}
			if tt.skip {
				sc.failOnSkip = true
				sc.platformSkipped["typstyle"] = struct{}{}
			}
			if tt.coverage {
				sc.opts.RequireCoverage = "unit"
				sc.narrowed["syncpack"] = struct{}{}
			}
			err := sc.outcome(ctx, tt.opErr)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("outcome() = %v, want nil", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("outcome() = %v, want an error containing %q", err, tt.want)
			}
			if tt.opErr != nil && !tt.interrupted {
				if coded, ok := errors.AsType[interface {
					error
					ExitCode() int
				}](err); ok {
					t.Errorf("outcome() = %v exits %d, want 1: a tool failure wins", err, coded.ExitCode())
				}
			}
			if tt.opErr == nil && !tt.interrupted && (tt.skip || tt.coverage) {
				if coded, ok := errors.AsType[interface {
					error
					ExitCode() int
				}](err); !ok || coded.ExitCode() != exitcode.Coverage {
					t.Errorf("outcome() = %v, want exit %d for an incomplete run", err, exitcode.Coverage)
				}
			}
		})
	}
}

func TestInterruptedErrorExitCode(t *testing.T) {
	tests := []struct {
		sig  syscall.Signal
		want int
		name string
	}{
		{sig: syscall.SIGINT, want: 130, name: "SIGINT"},
		{sig: syscall.SIGTERM, want: 143, name: "SIGTERM"},
	}
	for _, tt := range tests {
		err := interruptedError{sig: tt.sig}
		if got := err.ExitCode(); got != tt.want {
			t.Errorf("ExitCode() for %s = %d, want %d", tt.name, got, tt.want)
		}
		if err.Error() != "interrupted by "+tt.name {
			t.Errorf("Error() = %q, want it to name %s", err.Error(), tt.name)
		}
	}
	if interruption(context.Background()) != nil {
		t.Error("a live context reports an interruption")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if interruption(ctx) != nil {
		t.Error("a context cancelled without a signal reports an interruption")
	}
}

// A stopped task is listed apart from the results and counted in the footer as
// cancelled, never as failed; a planned task without a result is not started.
func TestRunSingleOperationReportsStoppedTasks(t *testing.T) {
	t.Setenv("CI", "true")
	var order []string
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{
		{Priority: 10, Tasks: []tooling.Task{task("alpha", ""), task("beta", "")}},
		{Priority: 20, Tasks: []tooling.Task{task("gamma", "")}},
	}}
	executor := &fakeExecutor{order: &order, results: []tooling.GroupExecutionResult{{Priority: 10, Results: []tooling.ExecutionResult{
		{ToolName: "alpha", Success: false, ExitCode: 1, FailureReason: tooling.FailureReasonIndependent},
		{ToolName: "beta", Cancelled: true, FailureReason: tooling.FailureReasonCancelled, StartedAt: time.Now()},
	}}}}
	sc := &sharedContext{
		planner:         &fakePlanner{plan: plan},
		executor:        executor,
		binMgr:          &fakeEnsurer{order: &order},
		timings:         timing.New(),
		platformSkipped: map[string]struct{}{},
		narrowed:        map[string]struct{}{},
		nameWidth:       5,
	}

	var err error
	out := captureStdout(t, func() { err = runSingleOperation(context.Background(), sc, config.OpLint) })
	if err == nil {
		t.Error("runSingleOperation() = nil, want the failure")
	}
	for _, want := range []string{
		"⊘ beta   cancelled (fail-fast)",
		"⊘ gamma  not started (fail-fast)",
		"1 tools · 1 runs · done in",
		"· 1 failed · 2 cancelled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
