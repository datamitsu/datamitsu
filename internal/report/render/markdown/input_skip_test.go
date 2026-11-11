package markdown

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

func TestParserSkippedInputRetainsItsFileAndReason(t *testing.T) {
	const file = "configuration.json"
	const reason = "unsupported validator profile"
	const category = "draft-profile"
	run := &report.Run{Operations: []report.Operation{{
		Name: "lint", Ran: true,
		Tools: []report.ToolRun{{Name: "schepherd", Invocations: []report.Invocation{{
			State: "ran", ExitCode: new(0),
			Skipped: []report.InputSkip{{File: file, Reason: reason, Category: category}},
		}}}},
	}}}
	var output bytes.Buffer
	if err := (Renderer{}).Render(&output, run, nil); err != nil {
		t.Fatal(err)
	}
	for _, detail := range []string{file, reason, category} {
		if !strings.Contains(output.String(), detail) {
			t.Errorf("skipped input lost %q in Markdown: %s", detail, output.String())
		}
	}
}
