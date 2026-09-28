// Package report holds the one record of a fix, lint or check run that every
// export is rendered from: which tools ran, on which files, what they found and
// how sure the core is about each of those. The runner folds each task's result
// into an Accumulator as it completes and builds a Run once the last operation
// has ended; renderers (internal/report/render) read nothing else.
//
// A Run carries no argv and no environment, and is never cached: it is a pure
// function of one run.
package report

import "time"

// SchemaVersion names the shape of a Run document. A reader rejects any other.
const SchemaVersion = "datamitsu.report/1"

// Millis is a duration in whole milliseconds.
type Millis int64

// Run is one run of fix, lint or check.
type Run struct {
	Schema    string    `json:"schema"`
	Datamitsu Producer  `json:"datamitsu"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
	Selection Selection `json:"selection"`
	// FailFast is the value the run used; a report turns it off.
	FailFast   bool        `json:"failFast"`
	Operations []Operation `json:"operations"`
	// Exports lists every report the run was asked for, with the status each
	// had when this document was written.
	Exports []Export `json:"exports"`
}

// Producer names the datamitsu build and the configuration a run came from.
type Producer struct {
	Version       string `json:"version"`
	Configuration string `json:"configuration"`
}

// Selection is what a run was asked to cover.
type Selection struct {
	// Mode is all, subtree, paths or empty (--file-scoped with nothing staged).
	Mode string `json:"mode"`
	// Dir is the subtree a subtree run stands in, relative to the repository
	// root.
	Dir string `json:"dir,omitempty"`
	// Paths are the paths a paths run named, relative to the repository root.
	Paths []string `json:"paths,omitempty"`
	// Tools is the --tools filter; empty when the run selected every tool.
	Tools      []string `json:"tools,omitempty"`
	FileScoped bool     `json:"fileScoped"`
}

// Operation is one operation of a run: fix or lint.
type Operation struct {
	Name string `json:"name"`
	// Ran is false for an operation the run never reached.
	Ran      bool   `json:"ran"`
	Success  bool   `json:"success"`
	Duration Millis `json:"durationMs"`
	// Skipped are the tools the planner left out, with the reason.
	Skipped []Skip `json:"skipped"`
	// Cancelled are the tasks the run stopped before they finished.
	Cancelled []Cancel  `json:"cancelled"`
	Tools     []ToolRun `json:"tools"`
}

// Skip is a tool the planner left out of an operation.
type Skip struct {
	Tool string `json:"tool"`
	// Reason is config, unsupported-platform or not-narrowable.
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// Cancel is a task the run stopped: cancelled after it started, or never
// started.
type Cancel struct {
	TaskID  string `json:"taskId"`
	Tool    string `json:"tool"`
	Dir     string `json:"dir"`
	Started bool   `json:"started"`
	// Cause is fail-fast or interrupted.
	Cause string `json:"cause"`
}

// ToolRun is everything one tool did in one operation.
type ToolRun struct {
	Name   string     `json:"name"`
	App    AppRef     `json:"app"`
	Parser *ParserRef `json:"parser"`
	// FailOn is the operation's effective threshold for this tool.
	FailOn string `json:"failOn"`
	// GateActive reports that the tool's parser module declares the severity
	// contract, so a finding at or above FailOn failed its invocation; under an
	// older module the exit code alone decided.
	GateActive bool `json:"gateActive"`
	// Category is the tool's category as its parser module describes it: ""
	// or security.
	Category    string       `json:"category"`
	Invocations []Invocation `json:"invocations"`
}

// AppRef is the app a tool ran, as configured.
type AppRef struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
}

// ParserRef is the output parser a tool declares.
type ParserRef struct {
	// Module is the parsers entry the tool names.
	Module string `json:"module"`
	// Parser is the parser key inside the module.
	Parser string `json:"parser"`
	// Version, Schema and ColumnUnit are what the module described itself as;
	// empty when the run never loaded it.
	Version    string `json:"version,omitempty"`
	Schema     int    `json:"schema,omitempty"`
	ColumnUnit string `json:"columnUnit,omitempty"`
}

// Invocation is one process a task planned, or what stood in for one: the
// files a cache answered, or a task that spawned nothing.
type Invocation struct {
	// ID is the process id "<taskId>#<n>", n counting the task's processes
	// from 1. "#0" stands for a task that spawned no process, "#cached" for
	// the files the per-file cache answered and "#verdict" for a unit whose
	// verdict held.
	ID          string `json:"id"`
	TaskID      string `json:"taskId"`
	Dir         string `json:"dir"`
	Scope       string `json:"scope"`
	Granularity string `json:"granularity"`
	Arity       string `json:"arity"`
	// Coverage is the task's: complete or partial.
	Coverage  string `json:"coverage"`
	WholeUnit bool   `json:"wholeUnit"`
	// State is ran, cached, verdict-hit, cancelled, not-started or
	// setup-failed.
	State string `json:"state"`
	// ExitCode is nil unless State is ran.
	ExitCode *int `json:"exitCode"`
	Success  bool `json:"success"`
	// FailureKind is "" for an invocation that did not fail, exit when the
	// tool failed on its own, threshold when a finding at or above failOn
	// failed a tool that exited 0, cancelled or setup.
	FailureKind string `json:"failureKind"`
	Duration    Millis `json:"durationMs"`
	// Extraction is what became of the output: parsed-clean,
	// parsed-findings, parser-unavailable, parse-failed, truncated or none.
	Extraction string `json:"extraction"`
	// Provenance is where the findings came from: parser or none.
	Provenance string       `json:"provenance"`
	Files      []FileResult `json:"files"`
	Findings   []Finding    `json:"findings"`
}

// FileResult is what became of one file an invocation answered for.
type FileResult struct {
	// Path is relative to the repository root with "/", or absolute outside it.
	Path     string `json:"path"`
	State    string `json:"state"`
	Success  bool   `json:"success"`
	ExitCode *int   `json:"exitCode"`
}

// Finding is one thing a tool reported.
type Finding struct {
	Tool     string `json:"tool"`
	Source   string `json:"source"`
	Code     string `json:"code,omitempty"`
	RuleURL  string `json:"ruleUrl,omitempty"`
	Severity string `json:"severity"`
	// Reported marks a finding at or above its operation's failOn: what the
	// terminal shows.
	Reported bool `json:"reported"`
	// Gates marks a finding that made its invocation fail.
	Gates bool `json:"gates"`
	// Kind is issue, or security for a tool whose category is security.
	Kind       string   `json:"kind"`
	Message    string   `json:"message"`
	Location   Location `json:"location"`
	Provenance string   `json:"provenance"`
}

// Location is where a finding points. Rows and columns are 1-based and EndCol
// is exclusive; a finding without a position has zeros.
type Location struct {
	// Path is relative to the repository root with "/", absolute outside it,
	// and "" for a finding that names no file.
	Path   string `json:"path"`
	Row    int    `json:"row"`
	EndRow int    `json:"endRow"`
	// Col and EndCol are in the parser's unit, as reported.
	Col    int `json:"col"`
	EndCol int `json:"endCol"`
	// Unit is the column unit the parser declares: utf-8, utf-16, utf-32, or
	// "" when it was not measured.
	Unit string `json:"unit"`
}

// Export is one report a run was asked for.
type Export struct {
	Format string `json:"format"`
	// Path is as given; "-" is stdout.
	Path string `json:"path"`
	// Status is written, failed, refused or omitted.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Export statuses.
const (
	ExportWritten = "written"
	ExportFailed  = "failed"
	ExportRefused = "refused"
	ExportOmitted = "omitted"
)
