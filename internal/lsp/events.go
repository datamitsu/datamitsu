package lsp

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// formatOp names a formatting request's phase and done events.
const formatOp = "format"

// emitLog writes a human-readable notice to the JSON-L stream. The server owns
// stdout for JSON-RPC and must add no notifications there, so stderr is where an
// editor finds out what the server decided.
func emitLog(opID, level, msg string) {
	ui.Emit(uievent.Event{Type: uievent.TypeLog, OpID: opID, Level: level, Msg: msg})
}

// formatTally is what a formatting request's done event summarizes.
type formatTally struct {
	tools   map[string]struct{}
	failed  map[string]struct{}
	runs    int
	skipped int // tasks left out by the editor policy or by the watchdog
}

// addResults counts executed tasks. Cancelled ones are fail-fast noise, as in
// the runner's footer.
func (t *formatTally) addResults(groups []tooling.GroupExecutionResult) {
	for _, group := range groups {
		for _, result := range group.Results {
			if cancelled(result) {
				continue
			}
			if t.tools == nil {
				t.tools = map[string]struct{}{}
				t.failed = map[string]struct{}{}
			}
			t.runs++
			t.tools[result.ToolName] = struct{}{}
			if !result.Success {
				t.failed[result.ToolName] = struct{}{}
			}
		}
	}
}

// emitFormatDone closes a request's format phase. Like the runner's done event,
// tools and failed count distinct tools and runs counts tasks.
func emitFormatDone(opID string, started time.Time, tally formatTally, err error) {
	success := err == nil && len(tally.failed) == 0
	ui.Emit(uievent.Event{
		Type:       uievent.TypeDone,
		OpID:       opID,
		Status:     terminalStatus(success),
		Op:         formatOp,
		Success:    new(success),
		DurationMs: time.Since(started).Milliseconds(),
		Tools:      len(tally.tools),
		Runs:       tally.runs,
		Failed:     len(tally.failed),
		Skipped:    tally.skipped,
	})
}

// leftOutNotice lists the tools the editor policy kept out of a save, each tool
// and reason once. Empty when nothing was left out.
func leftOutNotice(file string, left []leftOut) string {
	if len(left) == 0 {
		return ""
	}
	seen := make(map[leftOut]struct{}, len(left))
	parts := make([]string, 0, len(left))
	for _, l := range left {
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		parts = append(parts, fmt.Sprintf("%s (%s)", l.Tool, l.Reason))
	}
	return fmt.Sprintf("format %s: left out by the editor policy: %s", file, strings.Join(parts, ", "))
}

// watchdogNotice explains a save the watchdog cut short and how to avoid it.
func watchdogNotice(file string, ran, total, limitMs int, notRun []tooling.TaskGroup) string {
	return fmt.Sprintf(
		"format %s: stopped after %d of %d tool groups: the %dms format timeout elapsed; did not run: %s. "+
			"Raise datamitsu.format.timeoutMs / DATAMITSU_LSP_FORMAT_TIMEOUT_MS, or narrow format.widenTo",
		file, ran, total, limitMs, strings.Join(groupTools(notRun), ", "))
}

func groupTools(groups []tooling.TaskGroup) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, group := range groups {
		for _, task := range group.Tasks {
			if _, dup := seen[task.ToolName]; !dup {
				seen[task.ToolName] = struct{}{}
				names = append(names, task.ToolName)
			}
		}
	}
	return names
}

func countTasks(groups []tooling.TaskGroup) int {
	n := 0
	for _, group := range groups {
		n += len(group.Tasks)
	}
	return n
}

// toolRunOpID correlates a task's tool_run and error events with its request,
// in the runner's <op>:<tool>:<dir> shape.
func toolRunOpID(opID, tool, dir string) string {
	return opID + ":" + tool + ":" + dir
}

func terminalStatus(success bool) string {
	if success {
		return uievent.StatusDone
	}
	return uievent.StatusFail
}

func cancelled(result tooling.ExecutionResult) bool {
	return result.Cancelled || result.FailureReason == tooling.FailureReasonCancelled
}

const maxErrorOutput = 4 << 10

// resultErrorMessage is the underlying error when there is one, otherwise the
// exit code — as on the runner's error events — followed by the tail of the
// tool's output: an editor has no terminal, so this is the only place a user
// can see why a formatter failed.
func resultErrorMessage(result tooling.ExecutionResult) string {
	msg := fmt.Sprintf("exited with code %d", result.ExitCode)
	if result.Error != nil {
		msg = result.Error.Error()
	}
	out := strings.TrimSpace(result.Output)
	if out == "" {
		return msg
	}
	if len(out) > maxErrorOutput {
		cut := len(out) - maxErrorOutput
		for cut < len(out) && !utf8.RuneStart(out[cut]) {
			cut++
		}
		out = "…" + out[cut:]
	}
	return msg + "\n" + out
}
