package runner

import (
	"fmt"
	"strings"
	"unicode/utf8"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// stopCause says why a run stopped before every planned task finished.
type stopCause string

const (
	// stopFailFast: a task failed and fail-fast stopped the rest.
	stopFailFast stopCause = "fail-fast"
	// stopInterrupted: the run was interrupted (SIGINT, SIGTERM).
	stopInterrupted stopCause = "interrupted"
)

// stoppedTask is a planned task that did not run to completion: cancelled after
// it started, or never started at all. It is neither a pass nor a failure, and
// every output says which of the two it is, so that a consumer never reads
// "no output" as "clean".
type stoppedTask struct {
	tool    string
	dir     string
	started bool
	cause   stopCause
	// durationMs is how long a started task ran before it was stopped.
	durationMs int64
}

func (t stoppedTask) state() string {
	if t.started {
		return "cancelled"
	}
	return "not started"
}

func (t stoppedTask) eventMsg() string {
	return t.state() + ": " + string(t.cause)
}

func (t stoppedTask) label() string {
	if t.dir == "" {
		return t.tool
	}
	return t.tool + " [" + t.dir + "]"
}

// stoppedFromResult describes a task the executor returned as cancelled. A task
// cancelled while it waited for a worker never started, whatever its result says.
func stoppedFromResult(result tooling.ExecutionResult) stoppedTask {
	cause := stopFailFast
	if result.FailureReason == tooling.FailureReasonInterrupted {
		cause = stopInterrupted
	}
	return stoppedTask{
		tool:       result.ToolName,
		dir:        result.RelativeDir,
		started:    result.Started(),
		cause:      cause,
		durationMs: result.Duration,
	}
}

// taskKey identifies a planned task by what its result reports. Tasks of one
// per-file tool in one directory share a key; they are interchangeable here,
// so a count per key is enough to tell how many of them were never reached.
type taskKey struct{ tool, dir string }

// unreachedTasks lists, in plan order, the planned tasks the executor returned
// no result for: those in a priority group or a sequential sub-group the run
// never got to.
func unreachedTasks(plan *tooling.ExecutionPlan, results []tooling.GroupExecutionResult, taskDir func(tooling.Task) string, cause stopCause) []stoppedTask {
	reached := map[taskKey]int{}
	for _, group := range results {
		for _, r := range group.Results {
			reached[taskKey{r.ToolName, r.RelativeDir}]++
		}
	}
	var out []stoppedTask
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			key := taskKey{task.ToolName, taskDir(task)}
			if reached[key] > 0 {
				reached[key]--
				continue
			}
			out = append(out, stoppedTask{tool: key.tool, dir: key.dir, cause: cause})
		}
	}
	return out
}

// emitStopped writes a stopped task's terminal tool_run event. A task that
// started closes its start event with it; one that never started has only this
// event. Both are "skip", never "fail": a stopped task is not a failure.
func emitStopped(runOpID string, t stoppedTask) {
	ui.Emit(uievent.Event{
		Type:       uievent.TypeToolRun,
		OpID:       toolOpID(runOpID, t.tool, t.dir),
		Status:     uievent.StatusSkip,
		Tool:       t.tool,
		Dir:        t.dir,
		Msg:        t.eventMsg(),
		DurationMs: t.durationMs,
	})
}

// printStoppedTasks renders one faint "┃ ⊘ tool [dir]  cancelled (fail-fast)"
// line per stopped kind of task, in the style of the planner's skip lines. Tasks
// of one tool that share a directory, a state and a cause share a line with a
// ×N count: a per-file tool never reached over a thousand files is one fact,
// not a thousand lines.
func printStoppedTasks(stopped []stoppedTask, nameWidth int) {
	if ui.Quiet() || len(stopped) == 0 {
		return
	}
	type line struct {
		task  stoppedTask
		count int
	}
	type lineKey struct {
		tool, dir string
		started   bool
		cause     stopCause
	}
	var order []lineKey
	lines := map[lineKey]*line{}
	for _, t := range stopped {
		k := lineKey{t.tool, t.dir, t.started, t.cause}
		if l, ok := lines[k]; ok {
			l.count++
			continue
		}
		lines[k] = &line{task: t, count: 1}
		order = append(order, k)
	}
	for _, k := range order {
		l := lines[k]
		label := l.task.label()
		pad := max(nameWidth-utf8.RuneCountInString(label), 0) + 2
		text := fmt.Sprintf("%s (%s)", l.task.state(), l.task.cause)
		if l.count > 1 {
			text += fmt.Sprintf(" ×%d", l.count)
		}
		fmt.Println(clr.Faint("┃ ⊘ ") + clr.Faint(label) + strings.Repeat(" ", pad) + clr.Faint(text))
	}
}
