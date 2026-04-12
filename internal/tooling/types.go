package tooling

import (
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
)

// Task represents a single tool execution task
type Task struct {
	ToolName    string
	Tool        config.Tool
	Operation   config.OperationType
	OpConfig    config.ToolOperation
	Files       []string // Files to process (empty for whole-project mode)
	ProjectPath string   // Project root path for project-root working dir mode
}

// TaskGroup represents a group of tasks that can run in parallel
type TaskGroup struct {
	Priority int
	Tasks    []Task
}

// ExecutionPlan represents the full execution plan with ordered task groups
type ExecutionPlan struct {
	Groups []TaskGroup
}

// FailureReason indicates why a task failed, enabling the runner to distinguish
// independent tool failures from cascading terminations caused by fail-fast.
type FailureReason int

const (
	FailureReasonNone        FailureReason = iota // Task succeeded or not yet classified
	FailureReasonIndependent                      // Tool failed on its own
	FailureReasonCancelled                        // Tool terminated by fail-fast cascade
)

// ExecutionResult represents the result of a task execution
type ExecutionResult struct {
	ToolName      string
	Success       bool
	Output        string
	Error         error
	Duration      int64              // milliseconds
	Command       string             // Full command that was executed
	ExitCode      int                // Exit code of the command (0 if success, -1 if not available)
	WorkingDir    string             // Working directory where command was executed
	RelativeDir   string             // Working directory relative to git root (for display)
	Scope         config.ToolScope   // Tool scope (repository, per-project, per-file)
	Batch         bool               // Whether files were processed in batch mode
	Cancelled     bool               // Whether this task was cancelled by fail-fast
	FailureReason FailureReason      // Why the task failed (independent error vs cascading cancellation)
}

// GroupExecutionResult represents the result of a task group execution
type GroupExecutionResult struct {
	Priority          int
	Results           []ExecutionResult
	Success           bool
	WallClockDuration int64 // Wall-clock time in milliseconds (real time elapsed)
}

// GetToolNames returns a sorted list of unique tool names in the execution plan
func (p *ExecutionPlan) GetToolNames() []string {
	seen := make(map[string]bool)
	var names []string

	for _, group := range p.Groups {
		for _, task := range group.Tasks {
			if !seen[task.ToolName] {
				seen[task.ToolName] = true
				names = append(names, task.ToolName)
			}
		}
	}

	sort.Strings(names)
	return names
}
