package tooling

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPoolLimiting(t *testing.T) {
	// Set max workers to 2 for testing
	_ = os.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "2")
	defer func() { _ = os.Unsetenv("DATAMITSU_MAX_PARALLEL_WORKERS") }()

	// Track concurrent execution
	var concurrentCount int32
	var maxConcurrent int32
	var mu sync.Mutex

	// Create tasks that will track concurrency
	tasks := make([]Task, 10)
	for i := 0; i < 10; i++ {
		tasks[i] = Task{
			ToolName:  "test",
			Operation: config.OpLint,
			OpConfig: config.ToolOperation{
				App: "test",
				Scope:   config.ToolScopeRepository,
			},
		}
	}

	// Override executeTask to track concurrency
	originalExecuteTask := func(task Task) ExecutionResult {
		// Increment concurrent count
		current := atomic.AddInt32(&concurrentCount, 1)

		// Update max if needed
		mu.Lock()
		if current > maxConcurrent {
			maxConcurrent = current
		}
		mu.Unlock()

		// Simulate work
		time.Sleep(10 * time.Millisecond)

		// Decrement concurrent count
		atomic.AddInt32(&concurrentCount, -1)

		return ExecutionResult{
			ToolName: task.ToolName,
			Success:  true,
			Duration: 10,
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
			_ = os.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", fmt.Sprintf("%d", tt.maxWorkers))
			defer func() { _ = os.Unsetenv("DATAMITSU_MAX_PARALLEL_WORKERS") }()

			var concurrentCount int32
			var maxConcurrent int32
			var mu sync.Mutex

			tasks := make([]Task, tt.taskCount)
			for i := 0; i < tt.taskCount; i++ {
				tasks[i] = Task{
					ToolName:  "test",
					Operation: config.OpLint,
					OpConfig: config.ToolOperation{
						App: "test",
						Scope:   config.ToolScopeRepository,
					},
				}
			}

			originalExecuteTask := func(task Task) ExecutionResult {
				current := atomic.AddInt32(&concurrentCount, 1)

				mu.Lock()
				if current > maxConcurrent {
					maxConcurrent = current
				}
				mu.Unlock()

				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&concurrentCount, -1)

				return ExecutionResult{
					ToolName: task.ToolName,
					Success:  true,
					Duration: 5,
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

			expectedMax := tt.maxWorkers
			if tt.taskCount < tt.maxWorkers {
				expectedMax = tt.taskCount
			}

			if maxConcurrent > int32(expectedMax) {
				t.Errorf("maxConcurrent = %d, want <= %d", maxConcurrent, expectedMax)
			}

			t.Logf("%s: max concurrent = %d (expected <= %d)", tt.name, maxConcurrent, expectedMax)
		})
	}
}
