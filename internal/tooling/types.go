package tooling

import (
	"sort"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/textdiff"
	"github.com/datamitsu/datamitsu/internal/toolenv"
)

// Task represents a single tool execution task
type Task struct {
	// perFileCache is the name per-file cache entries are read and written
	// under, fixed before the tool starts (see perFileCacheTool).
	perFileCache string
	// inherited is OpConfig.InheritEnv captured from the host environment
	// once, before anything runs, so the cache identities and every process of
	// the task see the same values and the same absences.
	inherited toolenv.Inherited

	// ID is "<tool>:<dir>:<seq>", assigned by Execute before anything runs; seq
	// counts the tasks of that Execute from 1 in plan order. Unique within one
	// Execute, it is what tells two tasks of one tool in one directory apart.
	ID string

	ToolName    string
	Tool        config.Tool
	Operation   config.OperationType
	OpConfig    config.ToolOperation
	Files       []string // Files to process (empty for whole-project mode)
	ProjectPath string   // Project root path for project-root working dir mode

	// UnitDir is the directory whose verdict this task produces, relative to the
	// git root ("" is the root). Empty for file-granularity tasks, which have no
	// unit beyond the files themselves.
	UnitDir string
	// UnitMembers is every tracked file under UnitDir — not just the glob
	// matches. A superset is safe (a stale entry only costs a miss); a subset
	// would let an untracked-by-globs edit go unnoticed.
	UnitMembers []string
	// UnitGuards are inputs outside UnitDir that can still change the verdict:
	// ancestor configs, lock files, and config paths the args name.
	UnitGuards []string
	// Coverage records whether this task, as planned, covers its whole unit. Only
	// the planner sets it: the executor cannot tell a narrowed run from a full
	// one, and a run that guessed would record a verdict it did not earn.
	Coverage Coverage
}

// Coverage is whether a task covers the whole unit its verdict describes.
type Coverage string

// Coverage values.
const (
	CoverageComplete Coverage = "complete"
	CoveragePartial  Coverage = "partial"
)

// TaskGroup represents a group of tasks that can run in parallel
type TaskGroup struct {
	Priority int
	Tasks    []Task
}

// ExecutionPlan represents the full execution plan with ordered task groups
type ExecutionPlan struct {
	// ConfigName is the configuration this plan came out of, already defaulted.
	// A plan is only readable against the configuration that produced it, and a
	// machine may hold several.
	ConfigName string
	Groups     []TaskGroup
	// Skipped lists tools that were deliberately not planned, with the reason.
	// These never run but are reported so the user sees what was left out and why.
	Skipped []SkippedTool
}

// ManagedConfigRefs lists the managed configs the plan's tasks read.
func (p *ExecutionPlan) ManagedConfigRefs() []config.ManagedConfigRef {
	if p == nil {
		return nil
	}
	var refs []config.ManagedConfigRef
	for _, group := range p.Groups {
		for _, task := range group.Tasks {
			for _, ref := range task.OpConfig.ManagedConfigRefs {
				refs = append(refs, config.ManagedConfigRef{Tool: task.ToolName, Key: ref.Key})
			}
		}
	}
	return refs
}

// SkipReason classifies why a tool was skipped (vs. silently not applicable).
type SkipReason int

// Skip reason classifications. Other non-runs (project-type mismatch, no
// matching files, .datamitsuignore) stay silent.
const (
	// SkipReasonConfig marks a tool disabled via `skip: true` in config.
	SkipReasonConfig SkipReason = iota
	// SkipReasonUnsupportedPlatform marks a tool whose backing binary has no
	// build for the current os/arch/libc.
	SkipReasonUnsupportedPlatform
	// SkipReasonNotNarrowable marks an operation whose verdict covers the whole
	// repository, asked for from a subdirectory. It used to vanish silently.
	SkipReasonNotNarrowable
)

// String is the stable machine-readable key used in --explain=json.
func (r SkipReason) String() string {
	switch r {
	case SkipReasonConfig:
		return "config"
	case SkipReasonUnsupportedPlatform:
		return "unsupported-platform"
	case SkipReasonNotNarrowable:
		return "not-narrowable"
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
	case SkipReasonNotNarrowable:
		return "whole-repository verdict — cannot narrow"
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
	FailureReasonInterrupted                      // Tool terminated because the caller cancelled the run (a signal, a withdrawn request)
)

// Extraction is what became of one process's output: whether findings were
// extracted from it, and whether "no finding" can be believed. A consumer that
// replays a result as "nothing to report" needs more than the exit code, which
// is why every process records one.
type Extraction string

// Extraction outcomes of one process.
const (
	// ExtractionParsedClean: a parser ran without error and found nothing.
	ExtractionParsedClean Extraction = "parsed-clean"
	// ExtractionParsedFindings: a parser returned at least one finding.
	ExtractionParsedFindings Extraction = "parsed-findings"
	// ExtractionParserUnavailable: the declared module did not load, or does not
	// know the declared parser key, so nothing was parsed.
	ExtractionParserUnavailable Extraction = "parser-unavailable"
	// ExtractionParseFailed: the module returned an error for this output.
	ExtractionParseFailed Extraction = "parse-failed"
	// ExtractionTruncated: the output or the findings exceeded a cap. No cap
	// exists yet; the value is reserved so every consumer knows it.
	ExtractionTruncated Extraction = "truncated"
	// ExtractionNone: nothing was attempted — the tool declares no parser, or
	// the output is a formatter's file content, which no parser reads. A
	// declared parser the executor was not given is ExtractionParserUnavailable.
	ExtractionNone Extraction = "none"
)

// ParserUnavailableError is what a DiagnosticParser returns when it could not
// parse at all — the module did not load, or it does not list the parser key —
// as opposed to a module that parsed and failed.
type ParserUnavailableError struct {
	Err error
}

func (e *ParserUnavailableError) Error() string { return e.Err.Error() }

func (e *ParserUnavailableError) Unwrap() error { return e.Err }

// ProcessState is what became of one process a task planned to spawn.
type ProcessState string

// Process states.
const (
	// ProcessRan: the process ran and exited on its own, successfully or not.
	ProcessRan ProcessState = "ran"
	// ProcessCancelled: a cancellation stopped the process while it ran.
	ProcessCancelled ProcessState = "cancelled"
	// ProcessNotStarted: the task stopped before the process was spawned.
	ProcessNotStarted ProcessState = "not-started"
	// ProcessSetupFailed: the process could not be spawned — its input could not
	// be prepared, or the command did not start.
	ProcessSetupFailed ProcessState = "setup-failed"
)

// FileState is what became of one file a task was planned with.
type FileState string

// File states.
const (
	FileRan         FileState = "ran"
	FileCached      FileState = "cached"      // skipped: the per-file cache holds a pass for these bytes
	FileVerdictHit  FileState = "verdict-hit" // skipped: the unit's verdict holds for these inputs
	FileCancelled   FileState = "cancelled"
	FileNotStarted  FileState = "not-started"
	FileSetupFailed FileState = "setup-failed"
)

// outputTailBytes bounds ProcessResult.OutputTail.
const outputTailBytes = 4 << 10

// ProcessResult is one process a task planned to spawn: one per file in
// per-file mode, one per chunk in batch mode. Cached files have none.
type ProcessResult struct {
	// ID is "<TaskID>#<n>", n counting the task's processes from 1.
	ID string
	// Files are the absolute, cleaned paths the process was given; empty for a
	// process given no path.
	Files []string
	State ProcessState
	// ExitCode is nil unless State is ProcessRan.
	ExitCode *int
	// Success is whether the process did what it was run for: a zero exit and,
	// for a formatter, a formatted file written.
	Success     bool
	Extraction  Extraction
	ParseError  string // the module's error for parse-failed and parser-unavailable
	OutputTail  []byte // the last 4 KiB of the output the frame would show
	Diagnostics []diagnostic.Diagnostic
	DurationMs  int64

	edits []textdiff.Edit // the formatting edits a per-file process applied
}

// FileResult is what became of one file a task was planned with.
type FileResult struct {
	File  string
	State FileState
	// ProcessID names the process that checked the file; "" unless State is
	// FileRan.
	ProcessID string
	// Success is the process's success for a file that ran, and true for a file
	// a cache answered.
	Success bool
	// ExitCode is nil unless State is FileRan.
	ExitCode *int
	// Edits are the diff-in-core edits applied to the file; nil when it was left
	// unchanged.
	Edits []textdiff.Edit
}

// ExecutionResult represents the result of a task execution. Its aggregate
// fields speak for the task as a whole, the way a failure frame shows it;
// Processes and FileResults say what each process and each file did.
type ExecutionResult struct {
	ToolName string
	// TaskID is the ID of the task this result is for.
	TaskID  string
	Success bool
	// Output is the joined output of every process.
	Output   string
	Error    error
	Duration int64 // milliseconds
	// Command is the command line of the last failing process, or of the last
	// process when none failed.
	Command string
	// ExitCode is the last failing process's exit code: 0 on success, -1 when a
	// failure has none.
	ExitCode      int
	WorkingDir    string           // Working directory where command was executed
	RelativeDir   string           // Working directory relative to git root (for display)
	Scope         config.ToolScope // Tool scope (repository, per-project, per-file)
	Batch         bool             // Whether files were processed in batch mode
	Cancelled     bool             // Whether this task was cancelled, by fail-fast or an interruption (FailureReason says which)
	FailureReason FailureReason    // Why the task failed (independent error vs cascading cancellation)
	StartedAt     time.Time        // Absolute start of this run (zero if not timed)
	EndedAt       time.Time        // Absolute end of this run (zero if not timed)
	// FilesNotRun counts the files of a per-file task that fail-fast left unrun
	// when it stopped the loop at a failing file: the task failed on its own,
	// yet did not check everything it was given.
	FilesNotRun int
	// UnparsedFailures are the failed invocations of a task with an output
	// parser that left no diagnostic: a file whose input could not be prepared,
	// or a run — of one file, or of one chunk of a list — whose output held no
	// finding. A failure frame shows diagnostics instead of the raw output, so
	// it shows these beside them.
	UnparsedFailures []string
	// CapturedStdout holds the tool's stdout captured separately from stderr,
	// set only when the operation uses output mode "stdout" (the candidate
	// formatted content consumed by the diff-in-core formatting path). Empty for
	// the default combined-capture behavior.
	CapturedStdout string
	// Diagnostics holds the structured diagnostics parsed from this tool's output
	// when the tool declares an outputParser (and a parser is wired in): the
	// concatenation of every process's. Nil for tools without a parser — the
	// common case.
	Diagnostics []diagnostic.Diagnostic
	// Processes lists every process the task planned to spawn, in order: one
	// per file in per-file mode, one per chunk in batch mode.
	Processes []ProcessResult
	// ParseFailed reports that the output of at least one process could not be
	// parsed (parse-failed or parser-unavailable): an empty Diagnostics then
	// does not mean the tool found nothing.
	ParseFailed bool

	// Files are the task's files as planned, absolute and cleaned, run or
	// cached alike; for a WholeUnit task and for a verdict hit, the unit's
	// members.
	Files []string
	// FileResults has one entry per Files entry, in the same order.
	FileResults []FileResult
	// Cached reports that no process ran: the unit's verdict held, or every
	// file's per-file pass did.
	Cached bool
	// WholeUnit reports that argv carried no file path, so the result speaks for
	// UnitDir rather than for the files that selected the task.
	WholeUnit bool
	// UnitDir is the directory a WholeUnit result speaks for, relative to the
	// git root ("" is the root).
	UnitDir string

	cached []string // the files the per-file cache answered
}

// IsCancelled reports whether the task was stopped by a cancellation — fail-fast
// or an interruption — rather than failing on its own.
func (r *ExecutionResult) IsCancelled() bool {
	return r.Cancelled || r.FailureReason == FailureReasonCancelled || r.FailureReason == FailureReasonInterrupted
}

// Started reports whether the task reached execution. Every task that did has
// its timing recorded, whatever became of it; one cancelled while it waited for
// a worker has none.
func (r *ExecutionResult) Started() bool {
	return !r.StartedAt.IsZero()
}

// addProcess records one process the task spawned and folds its outcome into
// the task's aggregates.
func (r *ExecutionResult) addProcess(proc ProcessResult) {
	r.Processes = append(r.Processes, proc)
	r.Diagnostics = append(r.Diagnostics, proc.Diagnostics...)
	if proc.Extraction == ExtractionParseFailed || proc.Extraction == ExtractionParserUnavailable {
		r.ParseFailed = true
	}
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
