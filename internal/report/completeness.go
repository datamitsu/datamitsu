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

// toolReasons judges one tool run from its invocations.
func toolReasons(tr *ToolRun, sel Selection, parsed bool) []Reason {
	set := map[Reason]bool{}
	if sel.Mode != modeAll {
		set[ReasonNarrowedSelection] = true
	}
	for _, inv := range tr.Invocations {
		if inv.Coverage == string(tooling.CoveragePartial) {
			set[ReasonPartialUnit] = true
		}
		switch inv.State {
		case string(tooling.ProcessRan):
			if r, ok := extractionReason(tooling.Extraction(inv.Extraction)); ok {
				set[r] = true
			}
		case string(tooling.FileCached), string(tooling.FileVerdictHit):
			// A pass is recorded for a parsed tool only when its parser ran and
			// found nothing, so a hit of one replays a parsed-clean pass; a hit of
			// a tool without a parser replays an exit code.
			if !parsed {
				set[ReasonUnparsedCacheHit] = true
			}
		case string(tooling.ProcessCancelled):
			set[ReasonCancelled] = true
		case string(tooling.ProcessNotStarted):
			set[ReasonNotStarted] = true
		case string(tooling.ProcessSetupFailed):
			set[ReasonSetupFailed] = true
		}
	}
	return sortedReasons(set)
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
	if run.Selection.Mode != modeAll {
		set[ReasonNarrowedSelection] = true
	}
	if len(run.Selection.Tools) > 0 {
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

// judge sets the completeness of every tool run and of the run.
func judge(run *Run, tools config.MapOfTools) {
	complete := true
	for i := range run.Operations {
		op := &run.Operations[i]
		complete = complete && op.Ran
		for j := range op.Tools {
			tr := &op.Tools[j]
			set := map[Reason]bool{}
			for _, r := range tr.Incomplete {
				set[r] = true
			}
			for _, r := range toolReasons(tr, run.Selection, tools[tr.Name].OutputParser != nil) {
				set[r] = true
			}
			tr.Incomplete = sortedReasons(set)
			tr.Complete = len(tr.Incomplete) == 0
			complete = complete && tr.Complete
		}
	}
	run.Incomplete = runReasons(run)
	run.Complete = complete && len(run.Incomplete) == 0
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
