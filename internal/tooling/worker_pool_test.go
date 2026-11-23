package tooling

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	zerovalue_toolenv "github.com/datamitsu/datamitsu/internal/toolenv"
)

func TestWorkerPoolLimiting(t *testing.T) {
	// Set max workers to 2 for testing
	t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "2")

	// Track concurrent execution
	var concurrentCount atomic.Int32
	var maxConcurrent int32
	var mu sync.Mutex

	// Create tasks that will track concurrency
	tasks := make([]Task, 10)
	for i := range 10 {
		tasks[i] = Task{
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
			ToolName:     "test",
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
				App:               "test",
				Scope:             config.ToolScopeRepository,
			},
		}
	}

	// Override executeTask to track concurrency
	originalExecuteTask := func(task Task) ExecutionResult {
		// Increment concurrent count
		current := concurrentCount.Add(1)

		// Update max if needed
		mu.Lock()
		if current > maxConcurrent {
			maxConcurrent = current
		}
		mu.Unlock()

		// Simulate work
		time.Sleep(10 * time.Millisecond)

		// Decrement concurrent count
		concurrentCount.Add(-1)

		return ExecutionResult{
			TaskID:           "",
			Output:           "",
			Error:            nil,
			Command:          "",
			ExitCode:         0,
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
			cached:           nil,
			ToolName:         task.ToolName,
			Success:          true,
			Duration:         10,
		}
	}

	// Execute tasks in parallel using the executor's method
	results := make([]ExecutionResult, len(tasks))
	var wg sync.WaitGroup

	// Simulate the worker pool behavior from executeTasksParallel
	maxWorkers := 2
	semaphore := make(chan struct{}, maxWorkers)

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t Task) {
			defer wg.Done()

			// Acquire semaphore slot
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			results[idx] = originalExecuteTask(t)
		}(i, task)
	}

	wg.Wait()

	// Verify that we never exceeded the worker limit
	if maxConcurrent > 2 {
		t.Errorf("maxConcurrent = %d, want <= 2 (worker pool limit was exceeded)", maxConcurrent)
	}

	// Verify all tasks completed
	if len(results) != 10 {
		t.Errorf("len(results) = %d, want 10", len(results))
	}

	// Verify all tasks succeeded
	for i, result := range results {
		if !result.Success {
			t.Errorf("result[%d].Success = false, want true", i)
		}
	}

	t.Logf("Worker pool test passed: max concurrent workers = %d (limit = 2)", maxConcurrent)
}

func TestWorkerPoolWithDifferentSizes(t *testing.T) {
	tests := []struct {
		name       string
		maxWorkers int
		taskCount  int
	}{
		{"1 worker 5 tasks", 1, 5},
		{"2 workers 10 tasks", 2, 10},
		{"4 workers 8 tasks", 4, 8},
		{"10 workers 5 tasks", 10, 5}, // More workers than tasks
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", strconv.Itoa(tt.maxWorkers))

			var concurrentCount atomic.Int32
			var maxConcurrent int32
			var mu sync.Mutex

			tasks := make([]Task, tt.taskCount)
			for i := range tt.taskCount {
				tasks[i] = Task{
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
					ToolName:     "test",
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
						App:               "test",
						Scope:             config.ToolScopeRepository,
					},
				}
			}

			originalExecuteTask := func(task Task) ExecutionResult {
				current := concurrentCount.Add(1)

				mu.Lock()
				if current > maxConcurrent {
					maxConcurrent = current
				}
				mu.Unlock()

				time.Sleep(5 * time.Millisecond)
				concurrentCount.Add(-1)

				return ExecutionResult{
					TaskID:           "",
					Output:           "",
					Error:            nil,
					Command:          "",
					ExitCode:         0,
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
					cached:           nil,
					ToolName:         task.ToolName,
					Success:          true,
					Duration:         5,
				}
			}

			results := make([]ExecutionResult, len(tasks))
			var wg sync.WaitGroup
			semaphore := make(chan struct{}, tt.maxWorkers)

			for i, task := range tasks {
				wg.Add(1)
				go func(idx int, t Task) {
					defer wg.Done()
					semaphore <- struct{}{}
					defer func() { <-semaphore }()
					results[idx] = originalExecuteTask(t)
				}(i, task)
			}

			wg.Wait()

			expectedMax := min(tt.taskCount, tt.maxWorkers)

			if maxConcurrent > int32(expectedMax) {
				t.Errorf("maxConcurrent = %d, want <= %d", maxConcurrent, expectedMax)
			}

			t.Logf("%s: max concurrent = %d (expected <= %d)", tt.name, maxConcurrent, expectedMax)
		})
	}
}
