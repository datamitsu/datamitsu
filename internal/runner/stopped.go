package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

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
	taskID     string
	tool       string
	dir        string
	started    bool
	cause      stopCause
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

// cancel is the task as a report lists it.
func (t stoppedTask) cancel() report.Cancel {
	return report.Cancel{TaskID: t.taskID, Tool: t.tool, Dir: t.dir, Started: t.started, Cause: string(t.cause)}
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
		taskID:     result.TaskID,
		tool:       result.ToolName,
		dir:        result.RelativeDir,
		started:    result.Started(),
		cause:      cause,
		durationMs: result.Duration,
	}
}

// unreachedTasks lists, in plan order, the planned tasks the executor returned
// no result for: those in a priority group or a sequential sub-group the run
// never got to. Execute names every planned task before it runs any, and every
// result carries the name of its task.
func unreachedTasks(plan *tooling.ExecutionPlan, results []tooling.GroupExecutionResult, taskDir func(tooling.Task) string, cause stopCause) []stoppedTask {
	reached := map[string]bool{}
	for _, group := range results {
		for _, r := range group.Results {
			reached[r.TaskID] = true
		}
	}
	var out []stoppedTask
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			if reached[task.ID] {
				continue
			}
			out = append(out, stoppedTask{taskID: task.ID, tool: task.ToolName, dir: taskDir(task), cause: cause})
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
		OpID:       toolOpID(runOpID, t.taskID),
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
	if ui.Muted() || len(stopped) == 0 {
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
		fmt.Println(clr.Faint("┃ ⊘ ") + clr.Faint(toolText(label)) + strings.Repeat(" ", pad) + clr.Faint(text))
	}
}

// unrunFiles lists the files a task that ran did not check: the rest of a
// per-file loop fail-fast stopped at a failing file, or the chunks a
// cancellation reached before they started. A task the run stopped as a whole
// is listed by printStoppedTasks instead.
func unrunFiles(result tooling.ExecutionResult) []string {
	if result.IsCancelled() {
		return nil
	}
	var files []string
	for _, fr := range result.FileResults {
		if fr.State == tooling.FileNotStarted || fr.State == tooling.FileCancelled {
			files = append(files, fr.File)
		}
	}
	return files
}

// printUnrunFiles renders one faint "┃ ⊘ tool [dir]  2 files not run
// (fail-fast): a.txt, b.txt" line per task that left files unchecked, naming
// up to three of them relative to the repository root.
func printUnrunFiles(results []tooling.GroupExecutionResult, root string, nameWidth int, cause stopCause) {
	if ui.Muted() {
		return
	}
	const shown = 3
	for _, group := range results {
		for _, result := range group.Results {
			files := unrunFiles(result)
			if len(files) == 0 {
				continue
			}
			names := make([]string, 0, shown)
			for _, file := range files[:min(len(files), shown)] {
				if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
					file = filepath.ToSlash(rel)
				}
				names = append(names, file)
			}
			list := strings.Join(names, ", ")
			if len(files) > shown {
				list += fmt.Sprintf(" +%d more", len(files)-shown)
			}
			noun := "files"
			if len(files) == 1 {
				noun = "file"
			}
			label := stoppedTask{tool: result.ToolName, dir: result.RelativeDir}.label()
			pad := max(nameWidth-utf8.RuneCountInString(label), 0) + 2
			text := fmt.Sprintf("%d %s not run (%s): %s", len(files), noun, cause, list)
			fmt.Println(clr.Faint("┃ ⊘ ") + clr.Faint(toolText(label)) + strings.Repeat(" ", pad) + clr.Faint(toolText(text)))
		}
	}
}
