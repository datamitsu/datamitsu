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
	zerovalue_toolenv "github.com/datamitsu/datamitsu/internal/toolenv"
)

func TestFailureReasonIndependent_GetCommandInfoError(t *testing.T) {
	appManager := &mockAppManager{
		binaries: nil,
		commands: nil,
		err:      errors.New("binary not found"),
	}
	executor := NewExecutor("/root", false, false, appManager, nil)

	task := Task{
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		Files:        nil,
		ProjectPath:  "",
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     "missing-tool",
		Operation:    config.OpLint,
		OpConfig: config.ToolOperation{
			Args:              nil,
			Arity:             "",
			Granularity:       "",
			Globs:             nil,
			ExcludeGlobs:      nil,
			Priority:          0,
			Cache:             nil,
			InvalidateOn:      nil,
			Env:               nil,
			InheritEnv:        nil,
			FailOn:            "",
			Input:             "",
			LSP:               nil,
			Output:            "",
			ManagedConfigRefs: nil,
			App:               "missing-tool",
			Scope:             config.ToolScopeRepository,
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
		err:      nil,
		commands: nil,
		binaries: map[string]string{
			"failing-tool": scriptPath,
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		Files:        nil,
		ProjectPath:  "",
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     "failing-tool",
		Operation:    config.OpLint,
		OpConfig: config.ToolOperation{
			Arity:             "",
			Granularity:       "",
			Globs:             nil,
			ExcludeGlobs:      nil,
			Priority:          0,
			Cache:             nil,
			InvalidateOn:      nil,
			Env:               nil,
			InheritEnv:        nil,
			FailOn:            "",
			Input:             "",
			LSP:               nil,
			Output:            "",
			ManagedConfigRefs: nil,
			App:               "failing-tool",
			Args:              []string{},
			Scope:             config.ToolScopeRepository,
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
				err:      nil,
				commands: nil,
				binaries: map[string]string{
					"tool1": "/bin/true",
				},
			}
			executor := NewExecutor("/root", false, true, appManager, nil)

			tasks := []Task{
				{
					perFileCache: "",
					inherited:    zerovalue_toolenv.Inherited{},
					ID:           "",
					Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
					Files:        nil,
					UnitDir:      "",
					UnitMembers:  nil,
					UnitGuards:   nil,
					Coverage:     "",
					ToolName:     "tool1",
					Operation:    config.OpLint,
					ProjectPath:  filepath.Join("/root", "pkg", "a"),
					OpConfig: config.ToolOperation{
						Args:              nil,
						Arity:             "",
						Granularity:       "",
						Globs:             nil,
						ExcludeGlobs:      nil,
						Priority:          0,
						Cache:             nil,
						InvalidateOn:      nil,
						Env:               nil,
						InheritEnv:        nil,
						FailOn:            "",
						Input:             "",
						LSP:               nil,
						Output:            "",
						ManagedConfigRefs: nil,
						App:               "tool1",
						Scope:             config.ToolScopePerProject,
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
			if want := filepath.Join("pkg", "a"); r.RelativeDir != want || r.RelativeDir != executor.TaskDir(tasks[0]) {
				t.Errorf("RelativeDir = %q, want %q, the task's directory", r.RelativeDir, want)
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
		err:      nil,
		commands: nil,
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
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		ProjectPath:  "",
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     "slow-tool",
		Operation:    config.OpLint,
		OpConfig: config.ToolOperation{
			Arity:             "",
			Granularity:       "",
			Globs:             nil,
			ExcludeGlobs:      nil,
			Priority:          0,
			Cache:             nil,
			InvalidateOn:      nil,
			Env:               nil,
			InheritEnv:        nil,
			FailOn:            "",
			Input:             "",
			LSP:               nil,
			Output:            "",
			ManagedConfigRefs: nil,
			App:               "slow-tool",
			Args:              []string{"{file}"},
			Scope:             config.ToolScopePerFile,
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
		err:      nil,
		commands: nil,
		binaries: map[string]string{
			"good-tool": scriptPath,
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		perFileCache: "",
		inherited:    zerovalue_toolenv.Inherited{},
		ID:           "",
		Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		Files:        nil,
		ProjectPath:  "",
		UnitDir:      "",
		UnitMembers:  nil,
		UnitGuards:   nil,
		Coverage:     "",
		ToolName:     "good-tool",
		Operation:    config.OpLint,
		OpConfig: config.ToolOperation{
			Arity:             "",
			Granularity:       "",
			Globs:             nil,
			ExcludeGlobs:      nil,
			Priority:          0,
			Cache:             nil,
			InvalidateOn:      nil,
			Env:               nil,
			InheritEnv:        nil,
			FailOn:            "",
			Input:             "",
			LSP:               nil,
			Output:            "",
			ManagedConfigRefs: nil,
			App:               "good-tool",
			Args:              []string{},
			Scope:             config.ToolScopeRepository,
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
		binaries: nil,
		err:      nil,
		commands: map[string]*binmanager.CommandInfo{
			"fail-tool": {Args: nil, Env: nil, Artifact: "", ToolCacheDir: "", ToolCacheSlots: nil, RequiredPaths: nil, Type: "binary", Command: failScript},
			"slow-tool": {Args: nil, Env: nil, Artifact: "", ToolCacheDir: "", ToolCacheSlots: nil, RequiredPaths: nil, Type: "binary", Command: slowScript},
		},
	}

	executor := NewExecutor(tmpDir, false, true, appManager, nil) // failFast=true

	plan := &ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil,
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						perFileCache: "",
						inherited:    zerovalue_toolenv.Inherited{},
						ID:           "",
						Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
						ProjectPath:  "",
						UnitDir:      "",
						UnitMembers:  nil,
						UnitGuards:   nil,
						Coverage:     "",
						ToolName:     "fail-tool",
						Operation:    config.OpLint,
						OpConfig: config.ToolOperation{
							Arity:             "",
							Granularity:       "",
							ExcludeGlobs:      nil,
							Priority:          0,
							Cache:             nil,
							InvalidateOn:      nil,
							Env:               nil,
							InheritEnv:        nil,
							FailOn:            "",
							Input:             "",
							LSP:               nil,
							Output:            "",
							ManagedConfigRefs: nil,
							App:               "fail-tool",
							Args:              []string{},
							Scope:             config.ToolScopePerFile,
							Globs:             []string{"*.go"},
						},
						Files: []string{filepath.Join(tmpDir, "a.go")},
					},
					{
						perFileCache: "",
						inherited:    zerovalue_toolenv.Inherited{},
						ID:           "",
						Tool:         config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
						ProjectPath:  "",
						UnitDir:      "",
						UnitMembers:  nil,
						UnitGuards:   nil,
						Coverage:     "",
						ToolName:     "slow-tool",
						Operation:    config.OpLint,
						OpConfig: config.ToolOperation{
							Arity:             "",
							Granularity:       "",
							ExcludeGlobs:      nil,
							Priority:          0,
							Cache:             nil,
							InvalidateOn:      nil,
							Env:               nil,
							InheritEnv:        nil,
							FailOn:            "",
							Input:             "",
							LSP:               nil,
							Output:            "",
							ManagedConfigRefs: nil,
							App:               "slow-tool",
							Args:              []string{},
							Scope:             config.ToolScopePerFile,
							Globs:             []string{"*.ts"},
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
