package tooling

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailureReasonIndependent_GetCommandInfoError(t *testing.T) {
	appManager := &mockAppManager{
		err: fmt.Errorf("binary not found"),
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

func TestFailureReasonCancelled_ParallelTaskSkipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately so all tasks see cancellation

	appManager := &mockAppManager{
		binaries: map[string]string{
			"tool1": "/bin/true",
		},
	}
	executor := NewExecutor("/tmp", false, true, appManager, nil)

	tasks := []Task{
		{
			ToolName:  "tool1",
			Operation: config.OpLint,
			OpConfig: config.ToolOperation{
				App:   "tool1",
				Scope: config.ToolScopeRepository,
			},
		},
	}

	results := executor.executeTasksParallel(ctx, tasks, cancel)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Success {
		t.Fatal("expected failure")
	}
	if !results[0].Cancelled {
		t.Error("expected Cancelled = true")
	}
	if results[0].FailureReason != FailureReasonCancelled {
		t.Errorf("FailureReason = %d, want FailureReasonCancelled (%d)", results[0].FailureReason, FailureReasonCancelled)
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
	// The result should be classified as cancelled (the cancellation path in executePerFile
	// sets FailureReasonCancelled, and executeTask preserves it since it's not FailureReasonNone)
	if result.FailureReason != FailureReasonCancelled {
		t.Errorf("FailureReason = %d, want FailureReasonCancelled (%d)", result.FailureReason, FailureReasonCancelled)
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
