package cli_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes --report history: one line appended per run, counts only,
// written for a narrowed run and beside fail-fast.

var historyDurationRE = regexp.MustCompile(`"durationMs":\d+`)

func historyLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, strings.Count(raw, "\n"))
	for line := range strings.SplitSeq(strings.TrimSuffix(raw, "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("history line is not JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// TestReportHistory: a run appends one summary line to a file it never
// replaces; a second run appends a second.
func TestReportHistory(t *testing.T) {
	t.Run("appends", func(t *testing.T) {
		e := reportProject(t)
		for range 2 {
			e.wantExit(e.run("", nil, "lint", "--report", "history=trend/history.jsonl"), 0)
		}
		raw := e.read("trend/history.jsonl")
		lines := historyLines(t, raw)
		if len(lines) != 2 {
			t.Fatalf("history holds %d lines, want 2:\n%s", len(lines), raw)
		}
		if lines[0]["schema"] != "datamitsu.history/1" || lines[0]["startedAt"] != "2023-11-14T22:13:20Z" {
			t.Errorf("first line = %v", lines[0])
		}
		for _, leak := range []string{"Dockerfile", "DL3006", "Always tag"} {
			if strings.Contains(raw, leak) {
				t.Errorf("the history carries %q; a line holds counts only:\n%s", leak, raw)
			}
		}
		first, _, _ := strings.Cut(raw, "\n")
		clitest.AssertGolden(t, "report_history_line", historyDurationRE.ReplaceAllString(first, `"durationMs":<DUR>`)+"\n")
	})

	// A line holds no finding, so a narrowed run writes it, with its
	// selection, instead of being refused.
	t.Run("narrowed", func(t *testing.T) {
		e := reportProject(t)
		res := e.run("", nil, "lint", "--report", "history=h.jsonl", "Dockerfile")
		e.wantExit(res, 0)
		lines := historyLines(t, e.read("h.jsonl"))
		sel, _ := lines[0]["selection"].(map[string]any)
		if sel["mode"] != "paths" || lines[0]["complete"] != false {
			t.Errorf("line = %v, want an incomplete run of named paths", lines[0])
		}
		if strings.Contains(e.read("h.jsonl"), "Dockerfile") {
			t.Error("the line names the path the selection named")
		}
	})

	// A report of counts leaves fail-fast alone: it is neither turned off nor
	// refused beside --fail-fast=true.
	t.Run("fail_fast", func(t *testing.T) {
		e := reportProject(t)
		res := e.run("", nil, "lint", "--report", "history=h.jsonl", "--fail-fast=true")
		e.wantExit(res, 0)
		if lines := historyLines(t, e.read("h.jsonl")); lines[0]["failFast"] != true {
			t.Errorf("failFast = %v, want true", lines[0]["failFast"])
		}
		e.wantExit(e.run("", nil, "lint", "--report", "history=h.jsonl"), 0)
		if lines := historyLines(t, e.read("h.jsonl")); lines[1]["failFast"] != true {
			t.Errorf("failFast = %v, want the default, true: history does not turn it off", lines[1]["failFast"])
		}
	})
}
