package report

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// ShownMask decides which findings of one process the terminal shows — and so
// what every other consumer of "what a person sees" shows too: an agent's
// output, the annotations and the Markdown report. reported holds each
// finding's Reported flag. A failed process shows its findings at or above the
// threshold, or every one of them when none reaches it, so a failure always
// explains itself; a passed process shows its reported findings only when the
// threshold asked for was not enforced (Unenforced).
func ShownMask(failed, unenforced bool, reported []bool) []bool {
	mask := make([]bool, len(reported))
	anyReported := false
	for i, r := range reported {
		mask[i] = r && (failed || unenforced)
		anyReported = anyReported || r
	}
	if failed && !anyReported {
		for i := range mask {
			mask[i] = true
		}
	}
	return mask
}

// Unenforced reports a threshold other than the default that a parser module
// predating the severity contract left to the exit code.
func Unenforced(gateActive bool, failOn string) bool {
	return !gateActive && failOn != "" && failOn != string(config.DefaultFailOn)
}

// Visible returns the findings of inv the terminal shows (Finding.Shown) and
// the ones it counts instead. Synthetic findings are in neither: the terminal
// prints the tool's output in their place.
func Visible(inv Invocation) (shown, hidden []Finding) {
	for _, f := range inv.Findings {
		switch {
		case f.Kind == kindSynthetic:
		case f.Shown:
			shown = append(shown, f)
		default:
			hidden = append(hidden, f)
		}
	}
	return shown, hidden
}

// ShownOf is ShownMask over the findings of one process.
func ShownOf(proc tooling.ProcessResult) []bool {
	reported := make([]bool, len(proc.Diagnostics))
	for i, d := range proc.Diagnostics {
		reported[i] = d.Reported
	}
	failed := proc.State == tooling.ProcessRan && !proc.Success
	return ShownMask(failed, Unenforced(proc.GateActive, string(proc.FailOn)), reported)
}

// Synthetic returns the synthetic finding of inv, which stands for a failure
// that left no finding.
func Synthetic(inv Invocation) (Finding, bool) {
	for _, f := range inv.Findings {
		if f.Kind == kindSynthetic {
			return f, true
		}
	}
	return Finding{}, false
}
