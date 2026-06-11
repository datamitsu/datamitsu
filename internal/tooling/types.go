package tooling

import (
	"sort"
	"time"

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
	// Skipped lists tools that were deliberately not planned, with the reason.
	// These never run but are reported so the user sees what was left out and why.
	Skipped []SkippedTool
}

// SkipReason classifies why a tool was skipped (vs. silently not applicable).
type SkipReason int

// Skip reason classifications. Only these two reasons are surfaced to the user;
// other non-runs (project-type mismatch, no matching files, .datamitsuignore)
// stay silent.
const (
	// SkipReasonConfig marks a tool disabled via `skip: true` in config.
	SkipReasonConfig SkipReason = iota
	// SkipReasonUnsupportedPlatform marks a tool whose backing binary has no
	// build for the current os/arch/libc.
	SkipReasonUnsupportedPlatform
)

// String is the stable machine-readable key used in --explain=json.
func (r SkipReason) String() string {
	switch r {
	case SkipReasonConfig:
		return "config"
	case SkipReasonUnsupportedPlatform:
		return "unsupported-platform"
	}
	return "config"
}

// SkippedTool records a single tool that was skipped while planning one operation.
type SkippedTool struct {
	ToolName  string
	Operation config.OperationType
	Reason    SkipReason
	// Detail is the configured skipReason text (config) or the host string
	// (unsupported platform). May be empty for config skips with no reason set.
	Detail string
}

// ReasonText is the canonical human-facing reason for a skipped tool, shared by
// the run summary and the --explain formatters.
func (s SkippedTool) ReasonText() string {
	switch s.Reason {
	case SkipReasonConfig:
		if s.Detail != "" {
			return s.Detail
		}
		return "disabled in config"
	case SkipReasonUnsupportedPlatform:
		if s.Detail != "" {
			return "no binary for " + s.Detail
		}
		return "no binary for this platform"
	}
	return "disabled in config"
}

// FailureReason indicates why a task failed, enabling the runner to distinguish
// independent tool failures from cascading terminations caused by fail-fast.
type FailureReason int

// Failure reason classifications for a task result.
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
	Duration      int64            // milliseconds
	Command       string           // Full command that was executed
	ExitCode      int              // Exit code of the command (0 if success, -1 if not available)
	WorkingDir    string           // Working directory where command was executed
	RelativeDir   string           // Working directory relative to git root (for display)
	Scope         config.ToolScope // Tool scope (repository, per-project, per-file)
	Batch         bool             // Whether files were processed in batch mode
	Cancelled     bool             // Whether this task was cancelled by fail-fast
	FailureReason FailureReason    // Why the task failed (independent error vs cascading cancellation)
	StartedAt     time.Time        // Absolute start of this run (zero if not timed)
	EndedAt       time.Time        // Absolute end of this run (zero if not timed)
}

// recordTiming stamps the run's absolute wall-clock window and elapsed Duration
// from a single start time. Absolute timestamps let the reporter compute a
// tool's true wall-clock span (max end − min start) across parallel runs,
// rather than summing per-run durations (which over-counts heavily under
// parallelism, e.g. 57 parallel runs summing to 4m26s in a 33s run).
func (r *ExecutionResult) recordTiming(start time.Time) {
	end := time.Now()
	r.StartedAt = start
	r.EndedAt = end
	r.Duration = end.Sub(start).Milliseconds()
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

// GetAppNames returns a sorted list of unique app names referenced by the
// execution plan. Apps are the units that actually get installed/resolved by
// the BinManager (via GetCommandInfo), so this is what pre-install must use —
// a tool's name is a registry key and may differ from the app it executes.
// Empty app references are skipped.
func (p *ExecutionPlan) GetAppNames() []string {
	seen := make(map[string]bool)
	var names []string

	for _, group := range p.Groups {
		for _, task := range group.Tasks {
			app := task.OpConfig.App
			if app == "" || seen[app] {
				continue
			}
			seen[app] = true
			names = append(names, app)
		}
	}

	sort.Strings(names)
	return names
}
