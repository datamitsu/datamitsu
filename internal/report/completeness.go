package report

import (
	"slices"
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// Completeness is decided per tool from three facts, each failure adding a
// reason: scope (the run covered the whole repository and every task its whole
// unit), execution (every planned task ran to the end) and extraction (every
// output was read into findings, or a cache replayed a parsed-clean pass). A
// complete task in one unit says nothing about the units the selection left
// out, which is why scope needs the whole repository.

// toolReasons judges the same recorded evidence for a live run and for replay.
func toolReasons(tr *ToolRun, sel Selection, ran bool) []Reason {
	var reasons []Reason
	add := func(r Reason) {
		if r != "" && !slices.Contains(reasons, r) {
			reasons = append(reasons, r)
		}
	}
	if !sel.RepositoryWide() {
		add(ReasonNarrowedSelection)
	}
	if !ran {
		add(ReasonOperationSkipped)
	}
	if len(tr.Invocations) == 0 {
		add(ReasonMissingCompleteness)
	}
	for _, inv := range tr.Invocations {
		if inv.Coverage == string(tooling.CoveragePartial) {
			add(ReasonPartialUnit)
		}
		switch inv.ExecutionState() {
		case string(tooling.ProcessRan):
			if r, ok := extractionReason(inv.ExtractionState()); ok {
				add(r)
			} else if failedWithoutFindings(inv) {
				add(ReasonFailedWithoutFindings)
			}
		case string(tooling.FileCached), string(tooling.FileVerdictHit):
			if inv.ExtractionState() != tooling.ExtractionParsedClean {
				add(ReasonUnparsedCacheHit)
			}
		case string(tooling.ProcessCancelled):
			add(ReasonCancelled)
		case string(tooling.ProcessNotStarted):
			add(ReasonNotStarted)
		case string(tooling.ProcessSetupFailed):
			add(ReasonSetupFailed)
		default:
			add(ReasonMissingCompleteness)
		}
	}
	slices.Sort(reasons)
	return reasons
}

// failedWithoutFindings reports a process that failed on its own and whose
// parser answered with nothing: an answer that does not say what the tool
// found, which a report that lists findings would read as "clean" — and a
// service that tracks alerts would close every alert of the tool on. The
// fallback parser runs on exactly this answer once it exists.
func failedWithoutFindings(inv Invocation) bool {
	return inv.ExtractionState() == tooling.ExtractionParsedClean && inv.ExitCode != nil && *inv.ExitCode != 0
}

// Revise fills in evidence omitted by earlier producers and downgrades claims
// that contradict the recorded scope, execution or extraction. It never promotes
// an incomplete run or tool.
func Revise(run *Run) {
	if run.Fingerprint == "" {
		run.Fingerprint = FingerprintVersion
	}
	for i := range run.Operations {
		if op := &run.Operations[i]; !op.ChangesObserved && op.ChangesReason == "" {
			op.ChangesReason = ChangesNotRecorded
		}
	}
	checkCompleteness(run)
}

func extractionReason(e tooling.Extraction) (Reason, bool) {
	switch e {
	case tooling.ExtractionParsedClean, tooling.ExtractionParsedFindings:
		return "", false
	case tooling.ExtractionParserUnavailable:
		return ReasonParserUnavailable, true
	case tooling.ExtractionParseFailed:
		return ReasonParseFailed, true
	case tooling.ExtractionTruncated:
		return ReasonTruncated, true
	case tooling.ExtractionNone:
	}
	return ReasonNoExtraction, true
}

// runReasons are what the run as a whole left out. A narrowed selection is
// one of them as well as a reason of every tool, so a narrowed run that
// matched no tool is not complete either.
func runReasons(run *Run) []Reason {
	set := map[Reason]bool{}
	if !run.Selection.RepositoryWide() {
		set[ReasonNarrowedSelection] = true
	}
	if len(run.Selection.Tools) > 0 || len(run.Selection.ExcludedTools) > 0 {
		set[ReasonToolsFilter] = true
	}
	for _, op := range run.Operations {
		if !op.Ran {
			set[ReasonOperationSkipped] = true
		}
		for _, s := range op.Skipped {
			if s.Reason == tooling.SkipReasonNotNarrowable.String() {
				set[ReasonNotNarrowable] = true
			}
		}
	}
	return sortedReasons(set)
}

// judge starts a live run with optimistic claims, subject to recorded evidence.
func judge(run *Run) {
	run.Complete = true
	run.Incomplete = []Reason{}
	for i := range run.Operations {
		for j := range run.Operations[i].Tools {
			tr := &run.Operations[i].Tools[j]
			tr.Complete = len(tr.Incomplete) == 0
			if tr.Incomplete == nil {
				tr.Incomplete = []Reason{}
			}
		}
	}
	checkCompleteness(run)
}

func checkCompleteness(run *Run) {
	for _, reason := range runReasons(run) {
		addReason(&run.Incomplete, run.RedactedReasons, reason)
	}
	run.Complete = run.Complete && len(run.Incomplete) == 0
	for i := range run.Operations {
		op := &run.Operations[i]
		for j := range op.Tools {
			tr := &op.Tools[j]
			if tr.Complete && len(tr.Incomplete) == 0 {
				if reasons := toolReasons(tr, run.Selection, op.Ran); len(reasons) > 0 {
					tr.Incomplete = reasons
				}
			} else {
				// Preserve existing diagnostics on incomplete tools. Also recognize
				// the failed-clean case in reports that predate that reason.
				if !op.Ran {
					addReason(&tr.Incomplete, tr.RedactedReasons, ReasonOperationSkipped)
				}
				if slices.ContainsFunc(tr.Invocations, func(inv Invocation) bool {
					return inv.ExecutionState() == string(tooling.ProcessRan) && failedWithoutFindings(inv)
				}) {
					addReason(&tr.Incomplete, tr.RedactedReasons, ReasonFailedWithoutFindings)
				}
			}
			tr.Complete = tr.Complete && len(tr.Incomplete) == 0
			run.Complete = run.Complete && tr.Complete
		}
	}
}

func addReason(reasons *[]Reason, redacted []uint16, reason Reason) {
	for i, label := range *reasons {
		if effectiveReason(label, redacted, i) == reason {
			return
		}
	}
	*reasons = append(*reasons, reason)
	// Sorting would move labels away from their redaction codes.
	if len(redacted) == 0 {
		slices.Sort(*reasons)
	}
}

// IncompleteReason returns the reason's identity for decisions, not for display.
func (r *Run) IncompleteReason(i int) Reason {
	return effectiveReason(r.Incomplete[i], r.RedactedReasons, i)
}

// excludedTools are the configured tools with one of operations that are not
// in selected.
func excludedTools(tools config.MapOfTools, operations []string, selected []string) []string {
	if len(selected) == 0 {
		return nil
	}
	var out []string
	for name, tool := range tools {
		if slices.Contains(selected, name) {
			continue
		}
		for _, op := range operations {
			if _, ok := tool.Operations[config.OperationType(op)]; ok {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func sortedReasons(set map[Reason]bool) []Reason {
	out := make([]Reason, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}

const modeAll = "all"
