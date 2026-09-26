package runner

import (
	"fmt"
	"slices"
	"strings"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// opSummary is what one operation of a run reported once it ran: the numbers
// of its footer and of its done event. An operation that never reached its
// tools — fail-fast stopped the run before it, or its setup failed — has none.
type opSummary struct {
	op        config.OperationType
	tools     int
	runs      int
	failed    int
	skipped   int
	cancelled int
	// durationMs is the operation's execution time, the "done in" of its footer.
	durationMs int64
}

// recordOp keeps an operation's summary for the run-level report.
func (sc *sharedContext) recordOp(s opSummary) {
	sc.summaries = append(sc.summaries, s)
}

func (sc *sharedContext) summaryOf(op config.OperationType) (opSummary, bool) {
	i := slices.IndexFunc(sc.summaries, func(s opSummary) bool { return s.op == op })
	if i < 0 {
		return opSummary{}, false
	}
	return sc.summaries[i], true
}

// commandName is the command a list of operations was run by: check runs fix
// then lint, fix and lint run themselves.
func commandName(operations []config.OperationType) string {
	if len(operations) > 1 {
		return "check"
	}
	if len(operations) == 1 {
		return string(operations[0])
	}
	return ""
}

// printRunClosing prints check's closing rule: the wall clock of the whole
// command, each operation's execution time — or that it did not run — and the
// rest as setup (config load, walk, bundled checks, planning, installs, parser
// prewarm).
func (sc *sharedContext) printRunClosing(command string, operations []config.OperationType, elapsedMs int64) {
	if ui.Quiet() {
		return
	}
	parts := []string{command, "done in " + ui.FormatDurationShort(elapsedMs)}
	setup := elapsedMs
	for _, op := range operations {
		s, ran := sc.summaryOf(op)
		if !ran {
			parts = append(parts, string(op)+" not run")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", op, ui.FormatDurationShort(s.durationMs)))
		setup -= s.durationMs
	}
	parts = append(parts, "setup "+ui.FormatDurationShort(max(setup, 0)))

	rest := " · " + strings.Join(parts[1:], " · ")
	fmt.Println()
	fmt.Println(ui.RuleLine("┗", parts[0]+rest, clr.Bold(parts[0])+rest))
}

// emitRunDone writes the run-level done event: the sums over the operations
// that ran, the command's wall clock, and whether the run was complete — every
// planned operation ran and no task was cancelled or left unstarted. Its op_id
// starts with "cmd-", an operation's with "run-".
func (sc *sharedContext) emitRunDone(command string, operations []config.OperationType, elapsedMs int64, success bool) {
	var total opSummary
	complete := true
	for _, op := range operations {
		s, ran := sc.summaryOf(op)
		if !ran {
			complete = false
			continue
		}
		total.tools += s.tools
		total.runs += s.runs
		total.failed += s.failed
		total.skipped += s.skipped
		total.cancelled += s.cancelled
	}
	if total.cancelled > 0 {
		complete = false
	}
	ui.Emit(uievent.Event{
		Type:       uievent.TypeDone,
		OpID:       uievent.NextOpID("cmd"),
		Status:     doneStatus(success),
		Op:         command,
		Success:    new(success),
		DurationMs: elapsedMs,
		Tools:      total.tools,
		Runs:       total.runs,
		Failed:     total.failed,
		Skipped:    total.skipped,
		Cancelled:  new(total.cancelled),
		Complete:   new(complete),
	})
}
