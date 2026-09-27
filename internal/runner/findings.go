package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// levelCounts counts findings per level, error first.
type levelCounts [4]int

// String renders the non-zero counts, most serious first: "3 warnings, 1 info".
func (c *levelCounts) String() string {
	parts := make([]string, 0, len(c))
	for i, n := range c {
		if n == 0 {
			continue
		}
		level := diagnostic.Severity(i + 1)
		name := level.String()
		if n > 1 && level != diagnostic.SeverityInfo {
			name += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, name))
	}
	return strings.Join(parts, ", ")
}

func (c *levelCounts) add(s diagnostic.Severity) {
	if s >= diagnostic.SeverityError && s <= diagnostic.SeverityHint {
		c[s-1]++
	}
}

func (c *levelCounts) merge(o levelCounts) {
	for i := range c {
		c[i] += o[i]
	}
}

func (c *levelCounts) total() int {
	return c[0] + c[1] + c[2] + c[3]
}

// visibleFindings is what the terminal shows of one process: its findings at
// or above its operation's threshold. A failed process none of whose findings
// reaches the threshold shows every finding — a tool that fails on warnings
// explains itself. A passed process shows its reported findings only when the
// gate was not enforced for it although a threshold other than the default was
// asked for; otherwise a run that asked for nothing prints what it always did.
func visibleFindings(proc tooling.ProcessResult) []diagnostic.Diagnostic {
	var shown []diagnostic.Diagnostic
	for i, visible := range visibleMask(proc) {
		if visible {
			shown = append(shown, proc.Diagnostics[i])
		}
	}
	return shown
}

func visibleMask(proc tooling.ProcessResult) []bool {
	mask := make([]bool, len(proc.Diagnostics))
	failed := proc.State == tooling.ProcessRan && !proc.Success
	unenforced := !proc.GateActive && proc.FailOn != "" && proc.FailOn != config.DefaultFailOn
	anyReported := false
	for i, d := range proc.Diagnostics {
		mask[i] = d.Reported && (failed || unenforced)
		anyReported = anyReported || d.Reported
	}
	if failed && !anyReported {
		for i := range mask {
			mask[i] = true
		}
	}
	return mask
}

func sortFindings(ds []diagnostic.Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		switch {
		case a.Severity != b.Severity:
			return a.Severity < b.Severity
		case a.File != b.File:
			return a.File < b.File
		case a.Row != b.Row:
			return a.Row < b.Row
		case a.Col != b.Col:
			return a.Col < b.Col
		case a.Source != b.Source:
			return a.Source < b.Source
		default:
			return a.Code < b.Code
		}
	})
}

type taskView struct {
	// shown are the findings a frame prints, sorted.
	shown []diagnostic.Diagnostic
	// hidden counts the findings the terminal does not print.
	hidden levelCounts
	// unenforced marks a passed task whose findings print in a yellow frame:
	// a threshold was asked for, and a parser module that predates the
	// severity contract left the verdict to the exit code.
	unenforced bool
	// raw marks a frame that prints the tool's output instead of findings,
	// which then shows them all.
	raw bool
}

// viewOf decides what a frame shows of one task. --no-parse switches every
// frame to the tool's output, the yellow one included; a task that prints no
// frame keeps its counters, as nothing else shows its findings.
func viewOf(result tooling.ExecutionResult) taskView {
	var v taskView
	for _, proc := range result.Processes {
		for i, visible := range visibleMask(proc) {
			if visible {
				v.shown = append(v.shown, proc.Diagnostics[i])
			} else {
				v.hidden.add(proc.Diagnostics[i].Severity)
			}
		}
	}
	sortFindings(v.shown)
	v.unenforced = result.Success && len(v.shown) > 0
	if (!result.Success || v.unenforced) && (parsingDisabled() || !usableDiagnostics(result)) {
		v.raw, v.shown, v.hidden = true, nil, levelCounts{}
	}
	return v
}

// failOnOf is the threshold a result's findings were judged by.
func failOnOf(result tooling.ExecutionResult) config.Severity {
	if result.FailOn != "" {
		return result.FailOn
	}
	return config.DefaultFailOn
}
