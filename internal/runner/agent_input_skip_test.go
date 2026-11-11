package runner

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

func TestAgentReportsParserSkippedInputWithReason(t *testing.T) {
	const file = "configuration.json"
	const reason = "unsupported validator profile"
	const category = "draft-profile"
	var output strings.Builder
	var shown, hidden levelCounts
	baselined := 0
	agentInvocation(&output, report.ToolRun{Name: "schepherd"}, report.Invocation{
		State: "ran", ExitCode: new(0),
		Skipped: []report.InputSkip{{File: file, Reason: reason, Category: category}},
	}, &shown, &hidden, &baselined)
	for _, detail := range []string{"schepherd", file, reason, category} {
		if !strings.Contains(output.String(), detail) {
			t.Errorf("skipped input lost %q in agent output: %s", detail, output.String())
		}
	}
}
