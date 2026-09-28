// Package report holds the one record of a fix, lint or check run that every
// export is rendered from: which tools ran, on which files, what they found and
// how sure the core is about each of those. The runner folds each task's result
// into an Accumulator as it completes and builds a Run once the last operation
// has ended; renderers (internal/report/render) read nothing else.
//
// A Run carries no argv and no environment, and is never cached: it is a pure
// function of one run.
package report

import (
	"sort"
	"time"

	"github.com/datamitsu/datamitsu/internal/gitutil"

	"github.com/datamitsu/datamitsu/internal/textpos"
)

// SchemaVersion names the shape of a Run document. A reader rejects any other.
const SchemaVersion = "datamitsu.report/1"

// Millis is a duration in whole milliseconds.
type Millis int64

// Run is one run of fix, lint or check.
type Run struct {
	Schema string `json:"schema"`
	// Fingerprint is the version of the findings' fingerprints; a document
	// without it holds dmfp1, the first.
	Fingerprint string    `json:"fingerprint"`
	Datamitsu   Producer  `json:"datamitsu"`
	StartedAt   time.Time `json:"startedAt"`
	EndedAt     time.Time `json:"endedAt"`
	Selection   Selection `json:"selection"`
	// FailFast is the value the run used; a report turns it off.
	FailFast bool `json:"failFast"`
	// Complete is true when every tool run is complete, every operation ran
	// and Incomplete is empty. A document without it is never complete.
	Complete bool `json:"complete"`
	// Incomplete are the run-level reasons: narrowed-selection, tools-filter,
	// not-narrowable, operation-skipped.
	Incomplete []Reason    `json:"incomplete"`
	Operations []Operation `json:"operations"`
	// Exports lists every report the run was asked for, with the status each
	// had when this document was written.
	Exports []Export `json:"exports"`
	// CI is the continuous-integration job the run ran in; its vendor is ""
	// outside CI.
	CI CIEnvironment `json:"ci"`
}

// CIEnvironment identifies a CI job and the change it built: identifiers only.
type CIEnvironment struct {
	// Vendor is github, gitlab, azure, teamcity, buildkite, bitbucket,
	// jenkins, circleci, gitea, generic, or "".
	Vendor   string `json:"vendor"`
	SHA      string `json:"sha,omitempty"`
	Ref      string `json:"ref,omitempty"`
	BaseRef  string `json:"baseRef,omitempty"`
	PRNumber string `json:"prNumber,omitempty"`
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
	// ExcludedTools are the configured tools of the run's operations that
	// --tools left out.
	ExcludedTools []string `json:"excludedTools,omitempty"`
}

// Reason says why a tool run or a run is not complete.
type Reason string

// Reasons, by the fact that failed. Scope: the run was narrowed or a task
// covered part of its unit. Execution: a planned task did not run to the end.
// Extraction: an output was not read into findings. Run level: what the run as
// a whole left out.
const (
	ReasonNarrowedSelection Reason = "narrowed-selection"
	ReasonPartialUnit       Reason = "partial-unit"

	ReasonCancelled    Reason = "cancelled"
	ReasonNotStarted   Reason = "not-started"
	ReasonSetupFailed  Reason = "setup-failed"
	ReasonPlatformSkip Reason = "platform-skip"

	ReasonNoExtraction      Reason = "no-extraction"
	ReasonParserUnavailable Reason = "parser-unavailable"
	ReasonParseFailed       Reason = "parse-failed"
	ReasonTruncated         Reason = "truncated"
	ReasonUnparsedCacheHit  Reason = "unparsed-cache-hit"
	// ReasonFailedWithoutFindings: a process exited non-zero and the parser
	// that recognized its output found nothing in it: the failure is not in
	// what it printed. (An earlier build also recorded here an empty answer of
	// a module that could not say whether it recognized the output.)
	ReasonFailedWithoutFindings Reason = "failed-without-findings"

	ReasonToolsFilter      Reason = "tools-filter"
	ReasonNotNarrowable    Reason = "not-narrowable"
	ReasonOperationSkipped Reason = "operation-skipped"
)

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
	// ChangesObserved reports that the files the operation's tools changed
	// were observed, within ChangesScope: Changes and every invocation's
	// changes are all of them. When it is false, ChangesReason says why, and
	// no list stands for "nothing changed"; the changes seen before an
	// observation failed are kept.
	ChangesObserved bool          `json:"changesObserved"`
	ChangesReason   string        `json:"changesReason,omitempty"`
	ChangesDetail   string        `json:"changesDetail,omitempty"`
	ChangesScope    *ChangesScope `json:"changesScope,omitempty"`
	// Changes are the changed files no invocation of the step that changed
	// them was given.
	Changes []Change `json:"changes,omitempty"`
}

// ChangesScope is what the observation of an operation's changes covers.
type ChangesScope struct {
	// Tracked and Untracked files under the repository root are observed;
	// Ignored files and the contents of Submodules and nested repositories
	// are not.
	Tracked    bool `json:"tracked"`
	Untracked  bool `json:"untracked"`
	Ignored    bool `json:"ignored"`
	Submodules bool `json:"submodules"`
	// Renames says how a renamed file is reported: deleted+created.
	Renames string `json:"renames"`
}

// ObservedScope is the scope of every observation: the working tree as the
// version control status reports it.
var ObservedScope = ChangesScope{Tracked: true, Untracked: true, Renames: "deleted+created"}

// Change is one file an operation changed.
type Change struct {
	// Path is relative to the repository root with "/".
	Path string `json:"path"`
	// Kind is created, modified, deleted, or reverted: a file that differed
	// from the index before and matches it after.
	Kind string `json:"kind"`
	// Patch reports that the invocation's file result holds the change's
	// patch; a tool that writes files itself leaves none.
	Patch bool `json:"patch"`
	// Step is the step of the operation that made the change.
	Step int `json:"step"`
}

// AllChanges is every file op changed, once each, sorted by path: the net
// change of the steps that changed it, in the order they ran — a file created
// and then modified was created, one created and then deleted is not listed.
// Patch is set when any step's change of the file has one.
func (op Operation) AllChanges() []Change {
	all := append([]Change{}, op.Changes...)
	for _, tr := range op.Tools {
		for _, inv := range tr.Invocations {
			all = append(all, inv.Changes...)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Step < all[j].Step
	})
	var out []Change
	for i := 0; i < len(all); {
		j := i
		patch := false
		for j < len(all) && all[j].Path == all[i].Path {
			patch = patch || all[j].Patch
			j++
		}
		if c, ok := netChange(all[i:j]); ok {
			c.Patch = patch
			out = append(out, c)
		}
		i = j
	}
	return out
}

// netChange folds the changes of one file, in step order, into what changed
// from before the first to after the last; ok is false when the file neither
// existed before nor after.
func netChange(steps []Change) (Change, bool) {
	first, last := steps[0], steps[len(steps)-1]
	existed := first.Kind != gitutil.Created
	exists := last.Kind != gitutil.Deleted
	c := last
	switch {
	case !existed && !exists:
		return Change{}, false
	case !existed:
		c.Kind = gitutil.Created
	case !exists:
		c.Kind = gitutil.Deleted
	case last.Kind != gitutil.Reverted:
		c.Kind = gitutil.Modified
	}
	return c, true
}

// Why an operation's changes were not observed.
const (
	// ChangesNoFixTask: the operation holds no fix task — lint, or a fix
	// that planned nothing — so no snapshot was taken.
	ChangesNoFixTask = "no-fix-task"
	// ChangesNotRun: the run never reached the operation.
	ChangesNotRun = "not-run"
	// ChangesNotExecuted: no task ran — the tools could not be set up.
	ChangesNotExecuted = "not-executed"
	// ChangesNotRecorded: the document predates the observation of changes.
	ChangesNotRecorded = "not-recorded"
	// ChangesSnapshotFailed: the working tree's status could not be read —
	// no version control binary, no repository, or a failed status;
	// ChangesDetail says which.
	ChangesSnapshotFailed = "snapshot-failed"
)

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
	Category string `json:"category"`
	// Complete is true when Incomplete is empty: the tool covered the whole
	// repository, every planned task ran to the end, and every output was
	// read into findings.
	Complete    bool         `json:"complete"`
	Incomplete  []Reason     `json:"incomplete"`
	Invocations []Invocation `json:"invocations"`

	// mayHoldSecrets is set for a security tool, and for a tool with a parser
	// the run never described, whose category is unknown: its output is
	// withheld.
	mayHoldSecrets bool
}

// AppRef is the app a tool ran, as configured.
type AppRef struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	// OfficialURL is where a reader learns about the app: the one it declares,
	// or the one derived from its declaration.
	OfficialURL string `json:"officialUrl,omitempty"`
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
	// Provenance is what read the findings: parser (a tool's own parser),
	// format (a declared format parser), fallback:<format> (the fallback
	// built into datamitsu, and the format it recognized), or none.
	Provenance string       `json:"provenance"`
	Files      []FileResult `json:"files"`
	Findings   []Finding    `json:"findings"`
	// Step is the step of its operation the invocation ran in, counted from
	// 1: the invocations of one step ran together over disjoint files, after
	// every earlier step. It is recorded for a fix operation; 0 elsewhere.
	Step int `json:"step,omitempty"`
	// Changes are the files of the invocation its step changed.
	Changes []Change `json:"changes,omitempty"`
	// OutputTail is the last 4 KiB of what a failed invocation printed,
	// without ANSI sequences and masked; never for a tool whose category is
	// security. Only the own JSON carries it.
	OutputTail string `json:"outputTail,omitempty"`
}

// FileResult is what became of one file an invocation answered for.
type FileResult struct {
	// Path is relative to the repository root with "/", or absolute outside it.
	Path     string `json:"path"`
	State    string `json:"state"`
	Success  bool   `json:"success"`
	ExitCode *int   `json:"exitCode"`
	// Patch is the unified diff a formatter that writes its result on stdout
	// applied to the file, taken while both versions existed and masked; only
	// in a run asked for --report patch.
	Patch string `json:"patch,omitempty"`
}

// Finding is one thing a tool reported.
type Finding struct {
	// Fingerprint identifies the finding across runs, independently of the
	// lines around it: 64 lowercase hex characters (see Fingerprint).
	Fingerprint string `json:"fingerprint"`
	// FingerprintBasis is what the fingerprint rests on: line, row or none.
	FingerprintBasis string `json:"fingerprintBasis"`
	Tool             string `json:"tool"`
	Source           string `json:"source"`
	Code             string `json:"code,omitempty"`
	RuleURL          string `json:"ruleUrl,omitempty"`
	Severity         string `json:"severity"`
	// Reported marks a finding at or above its operation's failOn: what the
	// terminal shows.
	Reported bool `json:"reported"`
	// Gates marks a finding that made its invocation fail.
	Gates bool `json:"gates"`
	// Shown marks a finding the terminal shows, and so what agent output,
	// the annotations and Markdown list (ShownMask): decided in the process
	// that reported it, before duplicates across processes were dropped.
	Shown bool `json:"shown"`
	// Baselined marks a finding whose fingerprint the run's --baseline held:
	// it is neither reported nor gates, whatever its level.
	Baselined bool `json:"baselined,omitempty"`
	// Kind is issue, security for a tool whose category is security, or
	// synthetic for the one finding that stands for a tool that failed
	// without a parsable one.
	Kind       string   `json:"kind"`
	Message    string   `json:"message"`
	Location   Location `json:"location"`
	Provenance string   `json:"provenance"`

	// lineHash is the fingerprint's line component, kept to settle ordinals
	// across a tool's invocations.
	lineHash string
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
	// Chars, Bytes and Utf16 are the columns in code points, UTF-8 bytes and
	// UTF-16 units, computed while the file was on disk; absent when Precision
	// is unknown.
	Chars *textpos.Span `json:"chars,omitempty"`
	Bytes *textpos.Span `json:"bytes,omitempty"`
	Utf16 *textpos.Span `json:"utf16,omitempty"`
	// Precision is exact, ascii (no unit, but an ASCII line) or unknown (no
	// unit on a line that is not ASCII, or no line to read), in which case a
	// renderer that needs another unit leaves the column out.
	Precision string `json:"precision"`
}

// Values of Invocation.FailureKind: the tool failed on its own — a non-zero
// exit, or a result the executor rejected after it exited — a finding at or
// above failOn failed a tool that exited 0, the task was cancelled, or it could
// not be set up.
const (
	FailureExit      = "exit"
	FailureThreshold = "threshold"
	FailureCancelled = "cancelled"
	FailureSetup     = "setup"
)

// Export is one report a run was asked for.
type Export struct {
	Format string `json:"format"`
	// Path is as given; "-" is stdout.
	Path string `json:"path"`
	// Status is written, failed, refused or omitted.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Companion is the completeness companion written beside a report whose
	// format has no place to say how complete it is.
	Companion string `json:"companion,omitempty"`
	// Omitted are the tools a format that leaves out an incomplete tool left
	// out of the report, with why.
	Omitted []OmittedTool `json:"omitted,omitempty"`
}

// OmittedTool is a tool run a report left out.
type OmittedTool struct {
	Operation string `json:"operation"`
	Tool      string `json:"tool"`
	// Reasons are the tool run's incomplete reasons, or what else kept it out
	// of the format (too-many-results).
	Reasons []string `json:"reasons"`
}

// Export statuses.
const (
	ExportWritten = "written"
	ExportFailed  = "failed"
	ExportRefused = "refused"
	ExportOmitted = "omitted"
)
