package tooling

import "path/filepath"

// A cached pass is replayed as "nothing to report" — by the terminal, by an
// editor, by a report — so it may only be recorded where that is what the tool
// said (rule C1). The exit code alone cannot say it: a tool may exit 0 on
// findings below its own threshold, and a pass recorded then hides them on
// every later run. Every finding counts, whatever its level and whatever
// produced it (D7), for fix and lint alike: the failOn gate judges what a
// fixer leaves behind as it judges a linter's findings, so a pass recorded
// over a finding below one threshold would hide it from a stricter one.

// passesOf returns the files of covered that a successful process lets a pass
// be recorded for. A process whose output was parsed passes each file no
// finding or skip names, provided every finding and skip names one of covered.
// An unattributable skip could concern any file, so it blocks them all. A
// process with no parser declared keeps the exit-status rule. One whose output
// could not be parsed passes nothing.
func passesOf(proc ProcessResult, covered []string) []string {
	switch proc.Extraction {
	case ExtractionNone:
		return covered
	case ExtractionParsedClean, ExtractionParsedFindings:
	case ExtractionParserUnavailable, ExtractionParseFailed, ExtractionTruncated:
		return nil
	default:
		return nil
	}
	if proc.ExtractionPartial {
		return nil
	}
	if proc.InputPartial && len(proc.Skipped) == 0 {
		return nil
	}
	inProcess := make(map[string]bool, len(covered))
	for _, file := range covered {
		inProcess[file] = true
	}
	flagged := make(map[string]bool, len(proc.Diagnostics))
	for _, d := range proc.Diagnostics {
		if !inProcess[d.File] {
			return nil
		}
		flagged[d.File] = true
	}
	// Every skip must be attributable to one of this process's canonical paths.
	// Unknown skip records cannot safely exempt a single file from a clean pass.
	skipped := make(map[string]bool, len(proc.Skipped))
	for _, skip := range proc.Skipped {
		if skip.File == "" || !filepath.IsAbs(skip.File) || filepath.Clean(skip.File) != skip.File || !inProcess[skip.File] {
			return nil
		}
		skipped[skip.File] = true
	}
	passes := make([]string, 0, len(covered))
	for _, file := range covered {
		if !flagged[file] && !skipped[file] {
			passes = append(passes, file)
		}
	}
	return passes
}

// verdictEligible reports whether a successful task may record a unit verdict:
// every process ran — a dry run spawns none — and every one was parsed without
// a finding, or ran without an outputParser under the exit-status rule, and the
// task found nothing at all.
func verdictEligible(result ExecutionResult) bool {
	for _, proc := range result.Processes {
		if proc.State != ProcessRan {
			return false
		}
	}
	if len(result.Diagnostics) > 0 {
		return false
	}
	for _, proc := range result.Processes {
		if proc.InputPartial || proc.ExtractionPartial {
			return false
		}
		if proc.Extraction != ExtractionParsedClean && proc.Extraction != ExtractionNone {
			return false
		}
	}
	return true
}
