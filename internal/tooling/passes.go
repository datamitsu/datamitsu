package tooling

import "github.com/datamitsu/datamitsu/internal/config"

// A cached pass is replayed as "nothing to report" — by the terminal, by an
// editor, by a report — so for a lint operation it may only be recorded where
// that is what the tool said (rule C1). The exit code alone cannot say it: a
// tool may exit 0 on findings below its own threshold, and a pass recorded then
// hides them on every later run. Every finding counts, whatever its level and
// whatever produced it (D7).

// lintPasses returns the files of covered that a successful process lets a lint
// pass be recorded for. A process whose output was parsed passes each file no
// finding names, provided every finding names one of covered: a finding that
// names no file of the process — no file at all, another spelling, a path
// outside it — could be about any of them, and blocks them all. A process with
// no parser declared keeps the exit-status rule. One whose output could not be
// parsed passes nothing.
func lintPasses(proc ProcessResult, covered []string) []string {
	switch proc.Extraction {
	case ExtractionNone:
		return covered
	case ExtractionParsedClean, ExtractionParsedFindings:
	case ExtractionParserUnavailable, ExtractionParseFailed, ExtractionTruncated:
		return nil
	default:
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
	passes := make([]string, 0, len(covered))
	for _, file := range covered {
		if !flagged[file] {
			passes = append(passes, file)
		}
	}
	return passes
}

// passesOf returns the files a successful process lets a pass be recorded for.
// A fix pass follows success alone: the parser also runs over a fixer's output,
// which is not what it was written to read.
func passesOf(op config.OperationType, proc ProcessResult, covered []string) []string {
	if op != config.OpLint {
		return covered
	}
	return lintPasses(proc, covered)
}

// verdictEligible reports whether a successful task may record a unit verdict:
// every process ran — a dry run spawns none — and, for a lint operation, every
// one was parsed without a finding, or ran without an outputParser under the
// exit-status rule, and the task found nothing at all.
func verdictEligible(task Task, result ExecutionResult) bool {
	for _, proc := range result.Processes {
		if proc.State != ProcessRan {
			return false
		}
	}
	if task.Operation != config.OpLint {
		return true
	}
	if len(result.Diagnostics) > 0 {
		return false
	}
	for _, proc := range result.Processes {
		if proc.Extraction != ExtractionParsedClean && proc.Extraction != ExtractionNone {
			return false
		}
	}
	return true
}
