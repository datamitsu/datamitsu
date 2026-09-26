package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func TestFailureReasonIndependent_GetCommandInfoError(t *testing.T) {
	appManager := &mockAppManager{
		err: errors.New("binary not found"),
	}
	executor := NewExecutor("/root", false, false, appManager, nil)

	task := Task{
		ToolName:  "missing-tool",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App:   "missing-tool",
			Scope: config.ToolScopeRepository,
		},
	}

	result := executor.executeTask(context.Background(), task)

	if result.Success {
		t.Fatal("expected failure")
	}
	if result.FailureReason != FailureReasonIndependent {
		t.Errorf("FailureReason = %d, want FailureReasonIndependent (%d)", result.FailureReason, FailureReasonIndependent)
	}
}

func TestFailureReasonIndependent_ToolExitError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that exits with error
	scriptPath := filepath.Join(tmpDir, "fail.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	appManager := &mockAppManager{
		binaries: map[string]string{
			"failing-tool": scriptPath,
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		ToolName:  "failing-tool",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App:   "failing-tool",
			Args:  []string{},
			Scope: config.ToolScopeRepository,
		},
	}

	result := executor.executeTask(context.Background(), task)

	if result.Success {
		t.Fatal("expected failure")
	}
	if result.FailureReason != FailureReasonIndependent {
		t.Errorf("FailureReason = %d, want FailureReasonIndependent (%d)", result.FailureReason, FailureReasonIndependent)
	}
}

// A task that never got a worker is reported with the cause of the
// cancellation: fail-fast when the executor stopped the run, an interruption when
// the caller did. It carries its directory, so a caller can match it to the
// planned task, and no timing, because nothing ran.
func TestFailureReasonCancelled_ParallelTaskSkipped(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		want  FailureReason
	}{
		{name: "fail-fast", cause: errFailFast, want: FailureReasonCancelled},
		{name: "interrupted", cause: errors.New("interrupt signal received"), want: FailureReasonInterrupted},
		{name: "plain cancel", cause: nil, want: FailureReasonInterrupted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(tt.cause)

			appManager := &mockAppManager{
				binaries: map[string]string{
					"tool1": "/bin/true",
				},
			}
			executor := NewExecutor("/root", false, true, appManager, nil)

			tasks := []Task{
				{
					ToolName:    "tool1",
					Operation:   config.OpLint,
					ProjectPath: "/root/pkg/a",
					OpConfig: config.ToolOperation{
						App:   "tool1",
						Scope: config.ToolScopePerProject,
					},
				},
			}

			results := executor.executeTasksParallel(ctx, tasks, func() { t.Error("a cancelled task triggered fail-fast") })

			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			r := results[0]
			if r.Success {
				t.Fatal("expected failure")
			}
			if !r.Cancelled || !r.IsCancelled() {
				t.Error("expected the result to be cancelled")
			}
			if r.FailureReason != tt.want {
				t.Errorf("FailureReason = %d, want %d", r.FailureReason, tt.want)
			}
			if r.Started() {
				t.Error("a task that never got a worker reports that it started")
			}
			if r.RelativeDir != "pkg/a" || r.RelativeDir != executor.TaskDir(tasks[0]) {
				t.Errorf("RelativeDir = %q, want pkg/a, the task's directory", r.RelativeDir)
			}
		})
	}
}

func TestFailureReasonCancelled_PerFileCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that sleeps (will be cancelled)
	scriptPath := filepath.Join(tmpDir, "slow.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create test files
	for _, name := range []string{"a.js", "b.js"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	appManager := &mockAppManager{
		binaries: map[string]string{
			"slow-tool": scriptPath,
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay to let the first file start but second to be skipped
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	task := Task{
		ToolName:  "slow-tool",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App:   "slow-tool",
			Args:  []string{"{file}"},
			Scope: config.ToolScopePerFile,
		},
		Files: []string{
			filepath.Join(tmpDir, "a.js"),
			filepath.Join(tmpDir, "b.js"),
		},
	}

	result := executor.executeTask(ctx, task)

	if result.Success {
		t.Fatal("expected failure due to cancellation")
	}
	// The caller cancelled the context, not fail-fast: the run was interrupted.
	// executeTask keeps the classification because it is not FailureReasonNone.
	if result.FailureReason != FailureReasonInterrupted {
		t.Errorf("FailureReason = %d, want FailureReasonInterrupted (%d)", result.FailureReason, FailureReasonInterrupted)
	}
	if !result.IsCancelled() || !result.Started() {
		t.Errorf("IsCancelled() = %v, Started() = %v; want a started task that was cancelled", result.IsCancelled(), result.Started())
	}
}

func TestFailureReasonNone_Success(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "ok.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	appManager := &mockAppManager{
		binaries: map[string]string{
			"good-tool": scriptPath,
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		ToolName:  "good-tool",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App:   "good-tool",
			Args:  []string{},
			Scope: config.ToolScopeRepository,
		},
	}

	result := executor.executeTask(context.Background(), task)

	if !result.Success {
		t.Fatalf("expected success, got error: %v", result.Error)
	}
	if result.FailureReason != FailureReasonNone {
		t.Errorf("FailureReason = %d, want FailureReasonNone (%d)", result.FailureReason, FailureReasonNone)
	}
}

func TestFailureReasonCancelled_ExecuteFailFast(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that fails
	failScript := filepath.Join(tmpDir, "fail.sh")
	if err := os.WriteFile(failScript, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a script that sleeps
	slowScript := filepath.Join(tmpDir, "slow.sh")
	if err := os.WriteFile(slowScript, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"fail-tool": {Type: "binary", Command: failScript},
			"slow-tool": {Type: "binary", Command: slowScript},
		},
	}

	executor := NewExecutor(tmpDir, false, true, appManager, nil) // failFast=true

	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						ToolName:  "fail-tool",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App:   "fail-tool",
							Args:  []string{},
							Scope: config.ToolScopePerFile,
							Globs: []string{"*.go"},
						},
						Files: []string{filepath.Join(tmpDir, "a.go")},
					},
					{
						ToolName:  "slow-tool",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App:   "slow-tool",
							Args:  []string{},
							Scope: config.ToolScopePerFile,
							Globs: []string{"*.ts"},
						},
						Files: []string{filepath.Join(tmpDir, "b.ts")},
					},
				},
			},
		},
	}

	_, err := executor.Execute(context.Background(), plan)
	if err == nil {
		t.Fatal("expected error from fail-fast")
	}

	// Verify the error message mentions the failing tool but not the cancelled one
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("expected non-empty error message")
	}
	if !strings.Contains(errMsg, "fail-tool") {
		t.Errorf("error message should mention fail-tool, got: %s", errMsg)
	}
	if strings.Contains(errMsg, "slow-tool") {
		t.Errorf("error message should not mention cancelled slow-tool, got: %s", errMsg)
	}
}
