package runner

import (
	"context"
	"errors"
	"strings"
	zerovalue_sync "sync"
	"syscall"
	"testing"
	"time"

	zerovalue_cienv "github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

func task(tool, projectPath string) tooling.Task {
	return tooling.Task{
		Tool:        config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		Operation:   "",
		Files:       nil,
		UnitDir:     "",
		UnitMembers: nil,
		UnitGuards:  nil,
		Coverage:    "",
		ID:          tool + ":" + projectPath + ":1",
		ToolName:    tool,
		ProjectPath: projectPath,
		OpConfig:    config.ToolOperation{Args: nil, Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: tool, Scope: config.ToolScopeRepository},
	}
}

// relDir mirrors the executor's TaskDir for tasks whose ProjectPath is already
// relative: "" is the root.
func relDir(t tooling.Task) string { return t.ProjectPath }

// The planned tasks without a result are the ones the run never reached. Every
// planned task has an ID and every result carries its task's, so tasks of one
// tool in one directory are told apart, and the answer keeps plan order.
func TestUnreachedTasks(t *testing.T) {
	named := func(tool, dir, id string) tooling.Task {
		tk := task(tool, dir)
		tk.ID = id
		return tk
	}
	plan := &tooling.ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []tooling.TaskGroup{
			{Priority: 0, Tasks: []tooling.Task{named("fmt", "", "fmt::1"), named("fmt", "", "fmt::2"), named("tsc", "pkg/a", "tsc:pkg/a:3")}},
			{Priority: 0, Tasks: []tooling.Task{named("lint", "", "lint::4")}},
		},
	}
	results := []tooling.GroupExecutionResult{{
		Priority:          0,
		Success:           false,
		WallClockDuration: 0, Results: []tooling.ExecutionResult{
			{Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "fmt", TaskID: "fmt::2"},
			{Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "tsc", RelativeDir: "pkg/a", TaskID: "tsc:pkg/a:3"},
		},
	}}

	got := unreachedTasks(plan, results, relDir, stopFailFast)
	want := []stoppedTask{
		{dir: "", started: false, durationMs: 0, taskID: "fmt::1", tool: "fmt", cause: stopFailFast},
		{dir: "", started: false, durationMs: 0, taskID: "lint::4", tool: "lint", cause: stopFailFast},
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
		{Priority: 0, Success: false, WallClockDuration: 0, Results: []tooling.ExecutionResult{{TaskID: "", Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "a", FilesNotRun: 2}, {TaskID: "", Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "b"}}},
		{Priority: 0, Success: false, WallClockDuration: 0, Results: []tooling.ExecutionResult{{TaskID: "", Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "c", FilesNotRun: 1}}},
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
				TaskID:           "",
				Success:          false,
				Output:           "",
				Error:            nil,
				Command:          "",
				ExitCode:         0,
				WorkingDir:       "",
				Scope:            "",
				Batch:            false,
				EndedAt:          time.Time{},
				FilesNotRun:      0,
				UnparsedFailures: nil,
				CapturedStdout:   "",
				Diagnostics:      nil,
				Processes:        nil,
				ParseFailed:      false,
				FailOn:           "",
				Files:            nil,
				FileResults:      nil,
				Cached:           false,
				WholeUnit:        false,
				UnitDir:          "",
				ToolName:         "tsc", RelativeDir: "pkg/a", Cancelled: true,
				FailureReason: tooling.FailureReasonCancelled, StartedAt: started, Duration: 40,
			},
			want: stoppedTask{taskID: "", tool: "tsc", dir: "pkg/a", started: true, cause: stopFailFast, durationMs: 40},
			msg:  "cancelled: fail-fast",
		},
		{
			name: "waited for a worker",
			result: tooling.ExecutionResult{
				TaskID:           "",
				Success:          false,
				Output:           "",
				Error:            nil,
				Duration:         0,
				Command:          "",
				ExitCode:         0,
				WorkingDir:       "",
				Scope:            "",
				Batch:            false,
				StartedAt:        time.Time{},
				EndedAt:          time.Time{},
				FilesNotRun:      0,
				UnparsedFailures: nil,
				CapturedStdout:   "",
				Diagnostics:      nil,
				Processes:        nil,
				ParseFailed:      false,
				FailOn:           "",
				Files:            nil,
				FileResults:      nil,
				Cached:           false,
				WholeUnit:        false,
				UnitDir:          "",
				ToolName:         "tsc", RelativeDir: "pkg/b", Cancelled: true,
				FailureReason: tooling.FailureReasonCancelled,
			},
			want: stoppedTask{taskID: "", started: false, durationMs: 0, tool: "tsc", dir: "pkg/b", cause: stopFailFast},
			msg:  "not started: fail-fast",
		},
		{
			name: "interrupted",
			result: tooling.ExecutionResult{
				TaskID:           "",
				Success:          false,
				Output:           "",
				Error:            nil,
				Duration:         0,
				Command:          "",
				ExitCode:         0,
				WorkingDir:       "",
				RelativeDir:      "",
				Scope:            "",
				Batch:            false,
				EndedAt:          time.Time{},
				FilesNotRun:      0,
				UnparsedFailures: nil,
				CapturedStdout:   "",
				Diagnostics:      nil,
				Processes:        nil,
				ParseFailed:      false,
				FailOn:           "",
				Files:            nil,
				FileResults:      nil,
				Cached:           false,
				WholeUnit:        false,
				UnitDir:          "",
				ToolName:         "tsc", Cancelled: true,
				FailureReason: tooling.FailureReasonInterrupted, StartedAt: started,
			},
			want: stoppedTask{taskID: "", dir: "", durationMs: 0, tool: "tsc", started: true, cause: stopInterrupted},
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
		{taskID: "", durationMs: 0, tool: "tsc", dir: "pkg/a", started: true, cause: stopFailFast},
		{taskID: "", dir: "", started: false, durationMs: 0, tool: "fmt", cause: stopFailFast},
		{taskID: "", dir: "", started: false, durationMs: 0, tool: "fmt", cause: stopFailFast},
		{taskID: "", dir: "", started: false, durationMs: 0, tool: "fmt", cause: stopFailFast},
	}
	out := captureStdout(t, func() { printStoppedTasks(stopped, 5) })
	want := "┃ ⊘ tsc [pkg/a]  cancelled (fail-fast)\n┃ ⊘ fmt    not started (fail-fast) ×3\n"
	if out != want {
		t.Errorf("printStoppedTasks() printed\n%q\nwant\n%q", out, want)
	}
}

// TestPrintUnrunFiles: a task that ran and stopped short names the files it
// never checked; a task the run stopped as a whole is listed elsewhere.
func TestPrintUnrunFiles(t *testing.T) {
	t.Setenv("CI", "true")
	files := func(states ...tooling.FileState) []tooling.FileResult {
		out := make([]tooling.FileResult, len(states))
		for i, s := range states {
			out[i] = tooling.FileResult{ProcessID: "", Success: false, ExitCode: nil, Edits: nil, Patch: "", File: "/repo/src/f" + string(rune('0'+i)) + ".txt", State: s}
		}
		return out
	}
	results := []tooling.GroupExecutionResult{{
		Priority:          0,
		Success:           false,
		WallClockDuration: 0, Results: []tooling.ExecutionResult{
			{TaskID: "", Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "alpha", FileResults: files(tooling.FileRan, tooling.FileNotStarted)},
			{
				TaskID:           "",
				Success:          false,
				Output:           "",
				Error:            nil,
				Duration:         0,
				Command:          "",
				ExitCode:         0,
				WorkingDir:       "",
				Scope:            "",
				Batch:            false,
				Cancelled:        false,
				FailureReason:    0,
				StartedAt:        time.Time{},
				EndedAt:          time.Time{},
				FilesNotRun:      0,
				UnparsedFailures: nil,
				CapturedStdout:   "",
				Diagnostics:      nil,
				Processes:        nil,
				ParseFailed:      false,
				FailOn:           "",
				Files:            nil,
				Cached:           false,
				WholeUnit:        false,
				UnitDir:          "", ToolName: "beta", RelativeDir: "pkg", FileResults: files(
					tooling.FileRan, tooling.FileCancelled, tooling.FileNotStarted, tooling.FileNotStarted, tooling.FileNotStarted),
			},
			{TaskID: "", Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "gamma", Cancelled: true, FileResults: files(tooling.FileNotStarted)},
			{TaskID: "", Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "delta", FileResults: files(tooling.FileRan, tooling.FileCached)},
		},
	}}
	out := captureStdout(t, func() { printUnrunFiles(results, "/repo", 5, stopInterrupted) })
	want := "┃ ⊘ alpha  1 file not run (interrupted): src/f1.txt\n" +
		"┃ ⊘ beta [pkg]  4 files not run (interrupted): src/f1.txt, src/f2.txt, src/f3.txt +1 more\n"
	if out != want {
		t.Errorf("printUnrunFiles() printed\n%q\nwant\n%q", out, want)
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
		{interrupted: false, opErr: nil, skip: false, coverage: false, want: "", name: "clean"},
		{name: "interruption wins", interrupted: true, opErr: opErr, skip: true, coverage: true, want: "interrupted by SIGINT"},
		{interrupted: false, name: "tool failure wins, the assertions still reported", opErr: opErr, skip: true, coverage: true, want: "operation failed\n--fail-on-skip: 1 tool(s) have no binary for this host: typstyle\n--require-coverage=unit"},
		{interrupted: false, opErr: nil, name: "skip and coverage both reported", skip: true, coverage: true, want: "--fail-on-skip: 1 tool(s) have no binary for this host: typstyle\n--require-coverage=unit"},
		{interrupted: false, opErr: nil, coverage: false, name: "skip", skip: true, want: "--fail-on-skip"},
		{interrupted: false, opErr: nil, skip: false, name: "coverage", coverage: true, want: "--require-coverage=unit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			if tt.interrupted {
				cancel(interruptedError{sig: syscall.SIGINT})
			}
			sc := &sharedContext{cfg: nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, placementGated: false, startedAt: time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, platformSkipped: map[string]struct{}{}, narrowed: map[string]struct{}{}}
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

// An interruption between two groups that passed leaves no failed result, yet
// the operation did not complete: its done event fails.
func TestInterruptedOperationFails(t *testing.T) {
	t.Setenv("CI", "true")
	sink := &recordingSink{mu: zerovalue_sync.Mutex{}, events: nil}
	ui.SetEventSink(sink, true)
	defer ui.SetEventSink(nil, false)

	var order []string
	plan := &tooling.ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []tooling.TaskGroup{
			{Priority: 10, Tasks: []tooling.Task{task("alpha", "")}},
			{Priority: 20, Tasks: []tooling.Task{task("beta", "")}},
		},
	}
	executor := &fakeExecutor{
		called:   false,
		onResult: nil, order: &order, results: []tooling.GroupExecutionResult{
			{WallClockDuration: 0, Priority: 10, Success: true, Results: []tooling.ExecutionResult{{Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, FailureReason: 0, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "alpha", TaskID: "alpha::1", Success: true}}},
		},
	}
	sc := &sharedContext{
		cfg:             nil,
		rootPath:        "",
		cwdPath:         "",
		selection:       tooling.Selection{Mode: 0, Dir: "", Paths: nil},
		opts:            Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil},
		files:           nil,
		selectedTools:   nil,
		explainLevel:    "",
		projectCache:    nil,
		parserMgr:       nil,
		parseProblems:   nil,
		ignoredFailOn:   nil,
		nameWidth:       0,
		failOnSkip:      false,
		failFast:        false,
		afterFailedFix:  false,
		summaries:       nil,
		fileScoped:      false,
		report:          nil,
		secrets:         nil,
		annotator:       nil,
		baselineMatcher: nil,
		placement:       nil,
		placementGated:  false,
		startedAt:       time.Time{},
		ci:              zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		ciRuntime:       zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""},
		annotations:     annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false},
		planner:         &fakePlanner{plan: plan},
		executor:        executor,
		binMgr:          &fakeEnsurer{names: nil, called: false, err: nil, order: &order},
		timings:         timing.New(),
		platformSkipped: map[string]struct{}{},
		narrowed:        map[string]struct{}{},
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(interruptedError{sig: syscall.SIGINT})

	if err := runSingleOperation(ctx, sc, config.OpLint); err == nil {
		t.Error("runSingleOperation() = nil, want the interrupted operation to fail")
	}
	var done []uievent.Event
	for _, e := range sink.events {
		if e.Type == uievent.TypeDone {
			done = append(done, e)
		}
	}
	if len(done) != 1 || done[0].Status != uievent.StatusFail || done[0].Cancelled == nil || *done[0].Cancelled != 1 {
		t.Errorf("done events = %+v, want one fail counting beta as cancelled", done)
	}
}

// A failure whose joined output holds only blank lines shows its error, not an
// empty frame.
func TestFailedExecutionShowsTheErrorOverBlankOutput(t *testing.T) {
	t.Setenv("CI", "true")
	out := captureStdout(t, func() {
		printFailedExecution(1, executionInstance{
			relativeDir: "", result: tooling.ExecutionResult{
				TaskID:           "",
				Success:          false,
				Duration:         0,
				Command:          "",
				WorkingDir:       "",
				RelativeDir:      "",
				Scope:            "",
				Batch:            false,
				Cancelled:        false,
				FailureReason:    0,
				StartedAt:        time.Time{},
				EndedAt:          time.Time{},
				FilesNotRun:      0,
				UnparsedFailures: nil,
				CapturedStdout:   "",
				Diagnostics:      nil,
				Processes:        nil,
				ParseFailed:      false,
				FailOn:           "",
				Files:            nil,
				FileResults:      nil,
				Cached:           false,
				WholeUnit:        false,
				UnitDir:          "",
				ToolName:         "alpha", ExitCode: 1, Output: "\n\n", Error: errors.New("failed to execute for file a.txt (exit code 1)"),
			},
		})
	})
	if !strings.Contains(out, "failed to execute for file a.txt") {
		t.Errorf("the frame lacks the error:\n%s", out)
	}
}

// Diagnostics take the place of a failure's raw output, so the failures that
// left none are shown beside them.
func TestFailedExecutionShowsFailuresWithoutFindings(t *testing.T) {
	t.Setenv("CI", "true")
	out := captureStdout(t, func() {
		printFailedExecution(1, executionInstance{
			relativeDir: "", result: tooling.ExecutionResult{
				TaskID:         "",
				Success:        false,
				Error:          nil,
				Duration:       0,
				Command:        "",
				WorkingDir:     "",
				RelativeDir:    "",
				Scope:          "",
				Batch:          false,
				Cancelled:      false,
				FailureReason:  0,
				StartedAt:      time.Time{},
				EndedAt:        time.Time{},
				FilesNotRun:    0,
				CapturedStdout: "",
				ParseFailed:    false,
				FailOn:         "",
				Files:          nil,
				FileResults:    nil,
				Cached:         false,
				WholeUnit:      false,
				UnitDir:        "",
				ToolName:       "alpha", ExitCode: 1, Output: "finding\ncrashed",
				Processes: []tooling.ProcessResult{{
					ID:              "",
					Files:           nil,
					ExitCode:        nil,
					Success:         false,
					Extraction:      "",
					ParseError:      "",
					Provenance:      "",
					ParserModule:    "",
					OutputTail:      nil,
					DurationMs:      0,
					FailOn:          "",
					GateActive:      false,
					ThresholdFailed: false,
					State:           tooling.ProcessRan, Diagnostics: []diagnostic.Diagnostic{{Col: 0, EndRow: 0, EndCol: 0, Severity: 0, Source: "", Code: "", URL: "", Reported: false, Gates: false, Baselined: false, Anchor: nil, File: "found.txt", Row: 1, Message: "finding"}},
				}},
				Diagnostics:      []diagnostic.Diagnostic{{Col: 0, EndRow: 0, EndCol: 0, Severity: 0, Source: "", Code: "", URL: "", Reported: false, Gates: false, Baselined: false, Anchor: nil, File: "found.txt", Row: 1, Message: "finding"}},
				UnparsedFailures: []string{"crash.txt: exit code 2\ncrashed"},
			},
		})
	})
	for _, want := range []string{"finding", "crash.txt: exit code 2", "crashed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the frame lacks %q:\n%s", want, out)
		}
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
	plan := &tooling.ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []tooling.TaskGroup{
			{Priority: 10, Tasks: []tooling.Task{task("alpha", ""), task("beta", "")}},
			{Priority: 20, Tasks: []tooling.Task{task("gamma", "")}},
		},
	}
	executor := &fakeExecutor{
		called:   false,
		onResult: nil, order: &order, results: []tooling.GroupExecutionResult{{
			Success:           false,
			WallClockDuration: 0, Priority: 10, Results: []tooling.ExecutionResult{
				{Output: "", Error: nil, Duration: 0, Command: "", WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, Cancelled: false, StartedAt: time.Time{}, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "alpha", TaskID: "alpha::1", Success: false, ExitCode: 1, FailureReason: tooling.FailureReasonIndependent},
				{Success: false, Output: "", Error: nil, Duration: 0, Command: "", ExitCode: 0, WorkingDir: "", RelativeDir: "", Scope: "", Batch: false, EndedAt: time.Time{}, FilesNotRun: 0, UnparsedFailures: nil, CapturedStdout: "", Diagnostics: nil, Processes: nil, ParseFailed: false, FailOn: "", Files: nil, FileResults: nil, Cached: false, WholeUnit: false, UnitDir: "", ToolName: "beta", TaskID: "beta::1", Cancelled: true, FailureReason: tooling.FailureReasonCancelled, StartedAt: time.Now()},
			},
		}},
	}
	sc := &sharedContext{
		cfg:             nil,
		rootPath:        "",
		cwdPath:         "",
		selection:       tooling.Selection{Mode: 0, Dir: "", Paths: nil},
		opts:            Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil},
		files:           nil,
		selectedTools:   nil,
		explainLevel:    "",
		projectCache:    nil,
		parserMgr:       nil,
		parseProblems:   nil,
		ignoredFailOn:   nil,
		failOnSkip:      false,
		failFast:        false,
		afterFailedFix:  false,
		summaries:       nil,
		fileScoped:      false,
		report:          nil,
		secrets:         nil,
		annotator:       nil,
		baselineMatcher: nil,
		placement:       nil,
		placementGated:  false,
		startedAt:       time.Time{},
		ci:              zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		ciRuntime:       zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""},
		annotations:     annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false},
		planner:         &fakePlanner{plan: plan},
		executor:        executor,
		binMgr:          &fakeEnsurer{names: nil, called: false, err: nil, order: &order},
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
