package junit

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

func TestUnnamedParserSkipRemainsAttributedAndSkipped(t *testing.T) {
	const task = "schepherd:workspace:1"
	const reason = "unsupported validator profile"
	run := &report.Run{Operations: []report.Operation{{
		Name: "lint", Ran: true,
		Tools: []report.ToolRun{{
			Name: "schepherd", FailOn: "error",
			Invocations: []report.Invocation{{
				ID: task + "#1", TaskID: task, State: "ran", ExitCode: new(0),
				Skipped: []report.InputSkip{{Reason: reason}},
			}},
		}},
	}}}
	doc, _ := decode(t, run)
	suite := doc.suite(t, "lint/schepherd")
	if suite.Skipped != 1 || suite.Failures != 0 || suite.Errors != 0 {
		t.Fatalf("parser skip was not represented as a skipped outcome: %+v", suite)
	}
	for _, result := range suite.Cases {
		if result.Skipped == nil {
			continue
		}
		if !strings.Contains(result.Name, task) || !strings.Contains(result.Skipped.Message, reason) {
			t.Fatalf("unnamed skipped input lost task or reason: %+v", result)
		}
		if result.Failure != nil || result.Error != nil {
			t.Fatalf("skipped input also reported a failure: %+v", result)
		}
		return
	}
	t.Fatal("no skipped test case was emitted")
}
