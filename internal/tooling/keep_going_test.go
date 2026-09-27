package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
)

func shellApp(script string) *binmanager.CommandInfo {
	return &binmanager.CommandInfo{Type: "shell", Command: "/bin/sh", Args: []string{"-c", script}}
}

func lintTask(t *testing.T, tool string, scope config.ToolScope, projectPath string, args ...string) Task {
	t.Helper()
	if projectPath != "" {
		if err := os.MkdirAll(projectPath, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Task{
		ToolName:    tool,
		Operation:   config.OpLint,
		ProjectPath: projectPath,
		OpConfig:    config.ToolOperation{App: tool, Scope: scope, Args: args},
	}
}

func resultsByTool(groups []GroupExecutionResult) map[string]ExecutionResult {
	out := map[string]ExecutionResult{}
	for _, g := range groups {
		for _, r := range g.Results {
			out[r.ToolName] = r
		}
	}
	return out
}

// Without fail-fast every level runs to the end: later priority groups, later
// sequential sub-groups and parallel siblings all run after a failure, and the
// run still reports it.
func TestKeepGoingRunsEveryLevel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"fails":   shellApp("exit 1"),
		"slow":    shellApp("sleep 0.3"),
		"later":   shellApp("exit 0"),
		"overlap": shellApp("exit 0"),
	}}
	plan := &ExecutionPlan{Groups: []TaskGroup{
		{Priority: 10, Tasks: []Task{
			lintTask(t, "fails", config.ToolScopePerProject, filepath.Join(root, "a")),
			lintTask(t, "slow", config.ToolScopePerProject, filepath.Join(root, "b")),
			// Repository scope overlaps everything, so it forms a later sub-group.
			lintTask(t, "overlap", config.ToolScopeRepository, ""),
		}},
		{Priority: 20, Tasks: []Task{lintTask(t, "later", config.ToolScopeRepository, "")}},
	}}

	tests := []struct {
		failFast bool
		want     map[string]string
	}{
		{failFast: false, want: map[string]string{"fails": "failed", "slow": "passed", "overlap": "passed", "later": "passed"}},
		// fails may fail before or after slow finishes; slow is either killed or done,
		// never left out, and nothing after the group runs.
		{failFast: true, want: map[string]string{"fails": "failed", "overlap": "absent", "later": "absent"}},
	}
	for _, tt := range tests {
		t.Run(map[bool]string{true: "fail-fast", false: "keep-going"}[tt.failFast], func(t *testing.T) {
			e := NewExecutor(root, false, tt.failFast, appManager, nil)
			groups, err := e.Execute(context.Background(), plan)
			if tt.failFast != (err != nil) {
				t.Errorf("Execute() error = %v, want an error only under fail-fast", err)
			}
			if !tt.failFast && len(groups) != 2 {
				t.Fatalf("got %d group results, want 2", len(groups))
			}
			byTool := resultsByTool(groups)
			for tool, want := range tt.want {
				r, ok := byTool[tool]
				got := "absent"
				switch {
				case !ok:
				case r.Success:
					got = "passed"
				case r.IsCancelled():
					got = "cancelled"
				default:
					got = "failed"
				}
				if got != want {
					t.Errorf("%s: %s, want %s", tool, got, want)
				}
			}
			for _, g := range groups {
				if g.Priority == 10 && g.Success {
					t.Error("the group holding the failed task reports success")
				}
			}
		})
	}
}

// A running sibling is killed only by fail-fast. Without it the sibling
// finishes, and its result is not a cancellation.
func TestKeepGoingDoesNotKillSiblings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	done := filepath.Join(root, "done")
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"fails": shellApp("sleep 0.1; exit 1"),
		"slow":  shellApp("sleep 0.5; touch " + done),
	}}
	t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "2")
	e := NewExecutor(root, false, false, appManager, nil)
	results := e.executeTasksParallel(context.Background(), []Task{
		lintTask(t, "fails", config.ToolScopePerProject, filepath.Join(root, "a")),
		lintTask(t, "slow", config.ToolScopePerProject, filepath.Join(root, "b")),
	}, func() { t.Error("keep-going triggered fail-fast") })

	byTool := map[string]ExecutionResult{}
	for _, r := range results {
		byTool[r.ToolName] = r
	}
	if r := byTool["slow"]; !r.Success || r.IsCancelled() {
		t.Errorf("slow = %+v, want it to finish", r)
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("slow never finished: %v", err)
	}
}

// The per-file loop runs every file without fail-fast. The task fails, its exit
// code is the last failing file's, and the output of every file is kept.
func TestKeepGoingPerFileRunsEveryFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	names := []string{"bad1.txt", "ok.txt", "bad2.txt"}
	files := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	marker := filepath.Join(root, "ran")
	// sh -c hands the first argument after the script to it as $0.
	script := `echo "$0" >> ` + marker + `; case "$0" in *bad1*) echo "bad1 failed"; exit 3;; *bad2*) echo "bad2 failed"; exit 4;; esac`
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{"alpha": shellApp(script)}}

	tests := []struct {
		failFast bool
		wantRuns int
		wantExit int
		wantFile string
		wantOut  []string
		notRun   int
	}{
		{failFast: false, wantRuns: 3, wantExit: 4, wantFile: "bad2.txt", wantOut: []string{"bad1 failed", "bad2 failed"}},
		{failFast: true, wantRuns: 1, wantExit: 3, wantFile: "bad1.txt", wantOut: []string{"bad1 failed"}, notRun: 2},
	}
	for _, tt := range tests {
		t.Run(map[bool]string{true: "fail-fast", false: "keep-going"}[tt.failFast], func(t *testing.T) {
			_ = os.Remove(marker)
			task := lintTask(t, "alpha", config.ToolScopePerProject, root, "{file}")
			task.Files = files
			result := NewExecutor(root, false, tt.failFast, appManager, nil).executeTask(context.Background(), task)
			if result.Success || result.IsCancelled() {
				t.Fatalf("result = %+v, want a failure of its own", result)
			}
			if result.ExitCode != tt.wantExit {
				t.Errorf("ExitCode = %d, want %d (the last failing file's)", result.ExitCode, tt.wantExit)
			}
			if result.FilesNotRun != tt.notRun {
				t.Errorf("FilesNotRun = %d, want %d", result.FilesNotRun, tt.notRun)
			}
			if !strings.HasSuffix(result.Command, tt.wantFile) {
				t.Errorf("Command = %q, want the last failing file's, %s", result.Command, tt.wantFile)
			}
			ran, _ := os.ReadFile(marker)
			if n := strings.Count(string(ran), "\n"); n != tt.wantRuns {
				t.Errorf("the tool ran %d time(s), want %d:\n%s", n, tt.wantRuns, ran)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(result.Output, want) {
					t.Errorf("Output lacks %q:\n%s", want, result.Output)
				}
			}
		})
	}
}

// A file whose content cannot be read for a stdin tool fails without a
// process. Under keep-going the task goes on to the next file, and the failure
// keeps its own command and its explanation even when a later file passes.
func TestKeepGoingStdinFailureKeepsItsCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	ok := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(ok, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing.txt")
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{"alpha": shellApp(`cat >/dev/null; echo "$0 checked"`)}}
	task := lintTask(t, "alpha", config.ToolScopePerProject, root, "{file}")
	task.OpConfig.Input = config.ToolInputStdin
	task.Files = []string{missing, ok}

	result := NewExecutor(root, false, false, appManager, nil).executeTask(context.Background(), task)

	if result.Success || result.IsCancelled() {
		t.Fatalf("result = %+v, want the stdin failure", result)
	}
	if !strings.HasSuffix(result.Command, "missing.txt") || result.ExitCode != -1 {
		t.Errorf("Command = %q, ExitCode = %d; want missing.txt's command and no exit code", result.Command, result.ExitCode)
	}
	if !strings.Contains(result.Output, "failed to prepare stdin for file") || !strings.Contains(result.Output, "ok.txt checked") {
		t.Errorf("Output = %q, want the stdin failure beside ok.txt's output", result.Output)
	}
}

// A per-file task that failed on its own and is then interrupted stays a
// failure: the interruption only leaves the rest of its files unchecked.
func TestInterruptedPerFileTaskKeepsItsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	files := []string{filepath.Join(root, "bad.txt"), filepath.Join(root, "slow.txt"), filepath.Join(root, "last.txt")}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// sh -c hands the first argument after the script to it as $0. The loop
	// runs the files in order, so slow.txt starting proves bad.txt has failed.
	started := filepath.Join(root, "slow-started")
	script := `case "$0" in *bad*) echo "bad failed"; exit 3;; *slow*) touch ` + started + `; sleep 5;; esac`
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{"alpha": shellApp(script)}}
	task := lintTask(t, "alpha", config.ToolScopePerProject, root, "{file}")
	task.Files = files

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go func() {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				break
			}
		}
		cancel(errors.New("interrupt signal received"))
	}()
	result := NewExecutor(root, false, false, appManager, nil).executeTask(ctx, task)

	if result.Success || result.IsCancelled() {
		t.Fatalf("result = %+v, want the failure of bad.txt, not a cancellation", result)
	}
	if result.ExitCode != 3 || !strings.Contains(result.Output, "bad failed") {
		t.Errorf("ExitCode = %d, Output = %q; want bad.txt's failure", result.ExitCode, result.Output)
	}
	if result.FilesNotRun != 2 {
		t.Errorf("FilesNotRun = %d, want 2 (slow.txt was stopped, last.txt never ran)", result.FilesNotRun)
	}
}

// cancellingParser cancels the run while it parses, as a sibling failing under
// fail-fast would while a finished tool's output is still being parsed.
type cancellingParser struct{ cancel context.CancelCauseFunc }

func (p cancellingParser) Parse(context.Context, string, string, string, []byte, []byte, int32) ([]diagnostic.Diagnostic, error) {
	p.cancel(errFailFast)
	return []diagnostic.Diagnostic{{Message: "found it", Row: 1}}, nil
}

// A tool that failed on its own stays a failure, with its findings, when the
// run is cancelled after its process ended; only a process the cancellation
// stopped is a cancellation.
func TestFailureBeforeCancellationStaysAFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{"alpha": shellApp("exit 1")}}
	tests := []struct {
		name string
		args []string
	}{
		{name: "batch", args: nil},
		{name: "per-file", args: []string{"{file}"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(root, "a.txt")
			if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			e := NewExecutor(root, false, true, appManager, nil)
			e.SetParser(cancellingParser{cancel: cancel})
			task := lintTask(t, "alpha", config.ToolScopePerProject, root, tt.args...)
			task.Files = []string{file}
			task.Tool = config.Tool{Name: "alpha", OutputParser: &config.OutputParser{Module: "core", Parser: "alpha"}}

			result := e.executeTask(ctx, task)

			if result.Success || result.IsCancelled() || result.FailureReason != FailureReasonIndependent {
				t.Errorf("result = cancelled %v, reason %d; want an independent failure", result.IsCancelled(), result.FailureReason)
			}
			if len(result.Diagnostics) != 1 {
				t.Errorf("Diagnostics = %+v, want the parsed finding kept", result.Diagnostics)
			}
		})
	}
}

// ctxAppManager resolves a command only after the context is done, as a
// provisioning step would that the cancellation interrupted.
type ctxAppManager struct{}

func (ctxAppManager) GetBinaryPath(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (ctxAppManager) GetCommandInfo(ctx context.Context, _ string) (*binmanager.CommandInfo, error) {
	<-ctx.Done()
	return nil, fmt.Errorf("provision: %w", ctx.Err())
}

// A task whose command resolution the cancellation interrupted is cancelled,
// not a failure of its own.
func TestCancelledCommandResolutionIsACancellation(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		want  FailureReason
	}{
		{name: "fail-fast", cause: errFailFast, want: FailureReasonCancelled},
		{name: "interrupted", cause: errors.New("interrupt signal received"), want: FailureReasonInterrupted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(tt.cause)
			result := NewExecutor("/root", false, true, ctxAppManager{}, nil).
				executeTask(ctx, lintTask(t, "alpha", config.ToolScopeRepository, ""))
			if !result.IsCancelled() || result.FailureReason != tt.want || !result.Started() {
				t.Errorf("result = cancelled %v, reason %d, started %v; want a started task cancelled with reason %d",
					result.IsCancelled(), result.FailureReason, result.Started(), tt.want)
			}
		})
	}
}

// A batch task whose chunks ended differently is a failure when one chunk failed
// on its own, with the files of its cancelled chunks left unchecked, and a
// cancellation when every failed chunk was cancelled.
func TestMergeChunkResults(t *testing.T) {
	chunks := [][]string{{"a.go", "b.go"}, {"c.go"}, {"d.go", "e.go", "f.go"}}
	passed := ExecutionResult{Success: true}
	failed := ExecutionResult{Success: false, Error: errors.New("exit status 1"), FailureReason: FailureReasonIndependent}
	cancelled := ExecutionResult{Success: false, Error: errCancelled, Cancelled: true, FailureReason: FailureReasonCancelled}

	tests := []struct {
		name          string
		chunks        []ExecutionResult
		wantSuccess   bool
		wantCancelled bool
		wantNotRun    int
	}{
		{name: "all passed", chunks: []ExecutionResult{passed, passed, passed}, wantSuccess: true},
		{name: "a failure beside a cancellation", chunks: []ExecutionResult{failed, cancelled, passed}, wantNotRun: 1},
		{name: "failures beside cancellations", chunks: []ExecutionResult{cancelled, failed, cancelled}, wantNotRun: 5},
		{name: "only cancellations failed", chunks: []ExecutionResult{passed, cancelled, cancelled}, wantCancelled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(errFailFast)
			result := ExecutionResult{Success: true}
			for _, c := range tt.chunks {
				if !c.Success {
					result.Success = false
				}
			}
			mergeChunkResults(ctx, &result, chunks, tt.chunks)
			if result.Success != tt.wantSuccess || result.IsCancelled() != tt.wantCancelled || result.FilesNotRun != tt.wantNotRun {
				t.Errorf("result = success %v, cancelled %v, FilesNotRun %d; want %v, %v, %d",
					result.Success, result.IsCancelled(), result.FilesNotRun, tt.wantSuccess, tt.wantCancelled, tt.wantNotRun)
			}
		})
	}
}

// A sibling killed because another task failed is a fail-fast cancellation, and
// it had started: the two facts a caller needs to report it as cancelled rather
// than as never started. A tool that handles the stop signal and exits with a
// status of its own was stopped all the same.
func TestFailFastKilledSiblingIsStartedAndCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	tests := []struct {
		name string
		slow func(started string) string
	}{
		{
			name: "dies to the signal",
			slow: func(started string) string { return "touch " + started + "; sleep 5" },
		},
		{
			name: "handles the signal and exits 1",
			slow: func(started string) string {
				return "trap 'exit 1' TERM; touch " + started + "; i=0; while [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			// fails waits, at most five seconds, for slow's process to start.
			started := filepath.Join(root, "slow-started")
			appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
				"fails": shellApp(`i=0; while [ ! -f ` + started + ` ] && [ $i -lt 250 ]; do sleep 0.02; i=$((i+1)); done; exit 1`),
				"slow":  shellApp(tt.slow(started)),
			}}
			t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "2")
			e := NewExecutor(root, false, true, appManager, nil)
			start := time.Now()
			groups, err := e.Execute(context.Background(), &ExecutionPlan{Groups: []TaskGroup{{Priority: 1, Tasks: []Task{
				lintTask(t, "fails", config.ToolScopePerProject, filepath.Join(root, "a")),
				lintTask(t, "slow", config.ToolScopePerProject, filepath.Join(root, "b")),
			}}}})
			if err == nil {
				t.Fatal("Execute() = nil, want the fail-fast error")
			}
			if elapsed := time.Since(start); elapsed > 4*time.Second {
				t.Errorf("the run took %s; the sibling was not killed", elapsed)
			}
			slow := resultsByTool(groups)["slow"]
			if !slow.IsCancelled() || slow.FailureReason != FailureReasonCancelled || !slow.Started() {
				t.Errorf("slow = %+v, want a started task cancelled by fail-fast", slow)
			}
			if slow.RelativeDir != "b" {
				t.Errorf("slow.RelativeDir = %q, want b", slow.RelativeDir)
			}
		})
	}
}
