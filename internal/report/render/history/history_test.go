package history

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/report"
)

func sampleRun() *report.Run {
	one, zero := 1, 0
	return &report.Run{
		Schema:    report.SchemaVersion,
		Datamitsu: report.Producer{Version: "1.2.3", Configuration: "my config"},
		StartedAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
		Selection: report.Selection{Mode: "paths", Paths: []string{"secret/plan.md"}, Tools: []string{"eslint"}},
		FailFast:  true,
		CI:        report.CIEnvironment{Vendor: "github", SHA: "abc", Ref: "refs/heads/main", BaseRef: "main", PRNumber: "7"},
		Operations: []report.Operation{
			{
				Name: "lint", Ran: true, Duration: 1500,
				Tools: []report.ToolRun{
					{
						Name: "eslint", Complete: false, Incomplete: []report.Reason{report.ReasonNarrowedSelection},
						Invocations: []report.Invocation{
							{
								State: "ran", ExitCode: &one, FailureKind: report.FailureExit, Files: []report.FileResult{{Path: "src/a.ts"}},
								Findings: []report.Finding{
									{Severity: "error", Kind: "issue", Message: "x is unused", Location: report.Location{Path: "src/a.ts", Row: 3}},
									{Severity: "warning", Kind: "issue", Message: "prefer const"},
									{Severity: "hint", Kind: "issue"},
								},
							},
							{State: "ran", ExitCode: &zero, Success: true},
							{State: "cached", Success: true, Files: []report.FileResult{{Path: "src/b.ts"}, {Path: "src/c.ts"}}},
							{State: "cancelled", FailureKind: report.FailureCancelled},
						},
					},
					{
						Name: "tsc", Complete: false, Incomplete: []report.Reason{report.ReasonFailedWithoutFindings},
						Invocations: []report.Invocation{{
							State: "ran", ExitCode: &one, FailureKind: report.FailureExit,
							Findings: []report.Finding{{Severity: "error", Kind: "synthetic", Message: "tsc exited 1 without parsable findings"}},
						}},
					},
				},
			},
			{Name: "fix"},
		},
	}
}

func TestRenderLine(t *testing.T) {
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"datamitsu.history/1","startedAt":"2026-09-28T10:00:00Z",` +
		`"datamitsu":{"version":"1.2.3","configuration":"my config"},` +
		`"ci":{"vendor":"github","sha":"abc","ref":"refs/heads/main"},` +
		`"selection":{"mode":"paths","fileScoped":false,"toolsFiltered":true},` +
		`"complete":false,"failFast":true,"operations":[` +
		`{"name":"lint","ran":true,"success":false,"durationMs":1500,"tools":[` +
		`{"name":"eslint","runs":2,"cached":2,"failed":1,"complete":false,"incomplete":["narrowed-selection"],` +
		`"findings":{"error":1,"warning":1,"info":0,"hint":1}},` +
		`{"name":"tsc","runs":1,"cached":0,"failed":1,"complete":false,"incomplete":["failed-without-findings"],` +
		`"findings":{"error":0,"warning":0,"info":0,"hint":0}}]},` +
		`{"name":"fix","ran":false,"success":false,"durationMs":0,"tools":[]}]}` + "\n"
	if got := out.String(); got != want {
		t.Errorf("line =\n%s\nwant\n%s", got, want)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("a history entry is one line:\n%s", out.String())
	}
}

// TestLineCarriesNoFindingOrPath: a trend file is kept and shared, so a line
// holds counts only — none of a finding's text, path or the selection's paths.
func TestLineCarriesNoFindingOrPath(t *testing.T) {
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"src/a.ts", "secret/plan.md", "x is unused", "prefer const", "without parsable", `"prNumber"`, `"baseRef"`} {
		if strings.Contains(out.String(), leak) {
			t.Errorf("the line carries %q:\n%s", leak, out.String())
		}
	}
}

// TestDecodeRequiredKeys: a line decodes into the typed struct and carries
// every key a trend reads, at the top and per operation and tool.
func TestDecodeRequiredKeys(t *testing.T) {
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	line, err := Decode(bytes.TrimSpace(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if line.Schema != report.HistorySchema || len(line.Operations) != 2 || len(line.Operations[0].Tools) != 2 {
		t.Fatalf("decoded = %+v", line)
	}

	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "startedAt", "datamitsu", "ci", "selection", "complete", "failFast", "operations"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("line lacks %q", key)
		}
	}
	op := raw["operations"].([]any)[0].(map[string]any)
	for _, key := range []string{"name", "ran", "success", "durationMs", "tools"} {
		if _, ok := op[key]; !ok {
			t.Errorf("operation lacks %q", key)
		}
	}
	tool := op["tools"].([]any)[0].(map[string]any)
	for _, key := range []string{"name", "runs", "cached", "failed", "complete", "incomplete", "findings"} {
		if _, ok := tool[key]; !ok {
			t.Errorf("tool lacks %q", key)
		}
	}
	for _, key := range []string{"error", "warning", "info", "hint"} {
		if _, ok := tool["findings"].(map[string]any)[key]; !ok {
			t.Errorf("findings lack %q", key)
		}
	}
}

func TestDecodeRefusesOtherSchemas(t *testing.T) {
	for _, line := range []string{`{"schema":"datamitsu.history/2"}`, `{"schema":"datamitsu.report/1"}`, `not json`} {
		if _, err := Decode([]byte(line)); err == nil {
			t.Errorf("Decode(%s) = nil error", line)
		}
	}
}

// TestRenderIsStable: one run gives one line, byte for byte.
func TestRenderIsStable(t *testing.T) {
	var a, b bytes.Buffer
	if err := (Renderer{}).Render(&a, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	if err := (Renderer{}).Render(&b, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("two renders differ:\n%s\n%s", a.String(), b.String())
	}
}
