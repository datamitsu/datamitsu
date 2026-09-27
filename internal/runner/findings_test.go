package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// finding is one diagnostic at level, reported when it reaches failOn and
// gating when the gate was active.
func finding(level diagnostic.Severity, failOn config.Severity, active bool, message string) diagnostic.Diagnostic {
	reported := level <= diagnostic.Severity(failOn.Level())
	return diagnostic.Diagnostic{
		File: "/w/a.txt", Row: 1, Col: 1, Severity: level, Message: message,
		Reported: reported, Gates: reported && active,
	}
}

func process(ok, active bool, failOn config.Severity, findings ...diagnostic.Diagnostic) tooling.ProcessResult {
	return tooling.ProcessResult{
		State: tooling.ProcessRan, Success: ok, GateActive: active, FailOn: failOn,
		Extraction: tooling.ExtractionParsedFindings, Diagnostics: findings,
	}
}

func messages(ds []diagnostic.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Message)
	}
	return out
}

func TestVisibleFindings(t *testing.T) {
	e := config.SeverityError
	w := config.SeverityWarning
	cases := []struct {
		name string
		proc tooling.ProcessResult
		want []string
	}{
		{
			"a failed process shows what reaches the threshold",
			process(false, true, e, finding(diagnostic.SeverityError, e, true, "err"), finding(diagnostic.SeverityWarning, e, true, "warn")),
			[]string{"err"},
		},
		{
			"a failed process none of whose findings reaches it shows them all",
			process(false, true, e, finding(diagnostic.SeverityWarning, e, true, "w1"), finding(diagnostic.SeverityInfo, e, true, "i1")),
			[]string{"w1", "i1"},
		},
		{
			"a passed process shows nothing",
			process(true, true, e, finding(diagnostic.SeverityWarning, e, true, "warn")),
			nil,
		},
		{
			"an unenforced raised threshold shows what it would have caught",
			process(true, false, w, finding(diagnostic.SeverityWarning, w, false, "warn"), finding(diagnostic.SeverityInfo, w, false, "info")),
			[]string{"warn"},
		},
		{
			"an unenforced default threshold shows nothing",
			process(true, false, e, finding(diagnostic.SeverityError, e, false, "err")),
			nil,
		},
		{
			"a process no gate saw shows what it always did",
			tooling.ProcessResult{State: tooling.ProcessRan, Diagnostics: []diagnostic.Diagnostic{{Message: "m"}}},
			[]string{"m"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := messages(visibleFindings(c.proc)); !slices.Equal(got, c.want) {
				t.Errorf("visibleFindings = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLevelCountsString(t *testing.T) {
	cases := map[levelCounts]string{
		{}:            "",
		{0, 1, 0, 0}:  "1 warning",
		{0, 12, 3, 0}: "12 warnings, 3 info",
		{2, 0, 1, 5}:  "2 errors, 1 info, 5 hints",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", [4]int(c), got, want)
		}
	}
}

func frameOf(t *testing.T, result tooling.ExecutionResult) string {
	t.Helper()
	t.Setenv("CI", "true")
	group := toolExecutionGroup{
		toolName: result.ToolName, totalRuns: 1, minTime: -1, maxTime: -1,
		executions: []executionInstance{{result: result}},
	}
	if result.Success {
		group.succeededRuns = 1
	} else {
		group.failedRuns = 1
	}
	return captureStdout(t, func() {
		printGroupedResults([]toolExecutionGroup{group}, 10, false)
		printOperationFooter([]toolExecutionGroup{group}, 0, 0, 0, 0, 0, "")
	})
}

// TestFramesFollowTheThreshold covers what a run prints of its findings: the
// ones at or above the threshold, with the rest counted on the tool line, at
// the end of the frame and in the footer.
func TestFramesFollowTheThreshold(t *testing.T) {
	e, w := config.SeverityError, config.SeverityWarning
	withProcesses := func(ok bool, failOn config.Severity, processes ...tooling.ProcessResult) tooling.ExecutionResult {
		r := tooling.ExecutionResult{ToolName: "tool", Success: ok, ExitCode: 1, FailOn: failOn, Processes: processes}
		for _, p := range processes {
			r.Diagnostics = append(r.Diagnostics, p.Diagnostics...)
		}
		return r
	}

	t.Run("two errors and five warnings at the default", func(t *testing.T) {
		findings := make([]diagnostic.Diagnostic, 0, 7)
		findings = append(findings, finding(diagnostic.SeverityError, e, true, "e1"), finding(diagnostic.SeverityError, e, true, "e2"))
		for range 5 {
			findings = append(findings, finding(diagnostic.SeverityWarning, e, true, "w"))
		}
		out := frameOf(t, withProcesses(false, e, process(false, true, e, findings...)))
		for _, want := range []string{"e1", "e2", "+ 5 warnings hidden (failOn=error)", "· 5 warnings", "· 5 warnings hidden"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, " w\n") {
			t.Errorf("a warning below the threshold was printed:\n%s", out)
		}
	})

	t.Run("a failure with only warnings prints them all", func(t *testing.T) {
		out := frameOf(t, withProcesses(false, e, process(false, true, e,
			finding(diagnostic.SeverityWarning, e, true, "w1"), finding(diagnostic.SeverityWarning, e, true, "w2"))))
		if !strings.Contains(out, "w1") || !strings.Contains(out, "w2") || strings.Contains(out, "hidden") {
			t.Errorf("a failure without a reported finding should print all of them, hiding none:\n%s", out)
		}
	})

	t.Run("a raised threshold nobody enforced", func(t *testing.T) {
		out := frameOf(t, withProcesses(true, w, process(true, false, w,
			finding(diagnostic.SeverityWarning, w, false, "caught"), finding(diagnostic.SeverityInfo, w, false, "below"))))
		for _, want := range []string{"(failOn=warning not enforced)", "caught", "+ 1 info hidden (failOn=warning)"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("the default threshold nobody enforced", func(t *testing.T) {
		out := frameOf(t, withProcesses(true, e, process(true, false, e, finding(diagnostic.SeverityError, e, false, "err"))))
		if strings.Contains(out, "┌") || strings.Contains(out, "err\n") {
			t.Errorf("a run that asked for nothing printed a frame:\n%s", out)
		}
		if !strings.Contains(out, "· 1 error") {
			t.Errorf("the finding is not counted:\n%s", out)
		}
	})

	t.Run("a pass below the threshold is counted", func(t *testing.T) {
		out := frameOf(t, withProcesses(true, e, process(true, true, e, finding(diagnostic.SeverityWarning, e, true, "w"))))
		if strings.Contains(out, "┌") || !strings.Contains(out, "· 1 warning") || !strings.Contains(out, "· 1 warning hidden") {
			t.Errorf("want no frame and the warning counted on the tool line and in the footer:\n%s", out)
		}
	})

	t.Run("--no-parse keeps the counters of a pass it prints no frame for", func(t *testing.T) {
		t.Setenv("DATAMITSU_NO_PARSE", "1")
		out := frameOf(t, withProcesses(true, e, process(true, true, e, finding(diagnostic.SeverityWarning, e, true, "w"))))
		if strings.Contains(out, "┌") || !strings.Contains(out, "· 1 warning") || !strings.Contains(out, "· 1 warning hidden") {
			t.Errorf("want no frame and the warning counted on the tool line and in the footer:\n%s", out)
		}
	})

	t.Run("a threshold failure says so", func(t *testing.T) {
		proc := process(false, true, e, finding(diagnostic.SeverityError, e, true, "err"))
		proc.ThresholdFailed = true
		result := withProcesses(false, e, proc)
		result.ExitCode = 0
		out := frameOf(t, result)
		if !strings.Contains(out, "Failed on: 1 finding at or above failOn=error") {
			t.Errorf("the frame does not name the threshold:\n%s", out)
		}
	})
}
