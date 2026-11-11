package report

import (
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestAccumulatorDoesNotDeclareSkippedInputComplete(t *testing.T) {
	task := perFileTask("hadolint::1", "settings.yaml")
	processID := task.ID + "#1"
	accumulator := NewAccumulator(testOptions())
	operation := accumulator.BeginOperation("lint", &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{task}}}}, nil)
	operation.AddTask(tooling.ExecutionResult{
		ToolName: task.ToolName, TaskID: task.ID, Success: true, Files: task.Files,
		Processes: []tooling.ProcessResult{{
			ID: processID, Files: task.Files, State: tooling.ProcessRan, ExitCode: new(0), Success: true,
			Extraction: tooling.ExtractionParsedClean,
			Skipped:    []tooling.InputSkip{{File: abs("settings.yaml"), Reason: "unsupported schema", Category: "compatibility"}},
		}},
		FileResults: []tooling.FileResult{{File: abs("settings.yaml"), State: tooling.FileRan, ProcessID: processID, ExitCode: new(0), Success: true}},
	})
	operation.End(true, 1)
	run := accumulator.Build(BuildInfo{Selection: Selection{Mode: "all"}})
	tool := run.Operations[0].Tools[0]
	if run.Complete || tool.Complete || !slices.Contains(tool.Incomplete, ReasonInputSkipped) {
		t.Fatalf("a successful process concealed its skipped input: %+v", run)
	}
	invocation := tool.Invocations[0]
	if len(invocation.Skipped) != 1 {
		t.Fatalf("skipped input was lost: %+v", invocation)
	}
	if got := invocation.Skipped[0]; got.File != "settings.yaml" || got.Reason != "unsupported schema" || got.Category != "compatibility" {
		t.Fatalf("skipped input lost project-relative attribution: %+v", got)
	}
}
