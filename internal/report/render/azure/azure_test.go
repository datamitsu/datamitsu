package azure

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	zerovalue_time "time"

	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/github"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

// TestEscape is the agent's table: "%" first, then ";", CR, LF and "]".
func TestEscape(t *testing.T) {
	tests := map[string]string{
		"100%":         "100%AZP25",
		"a;b":          "a%3Bb",
		"a\rb":         "a%0Db",
		"a\nb":         "a%0Ab",
		"a]b":          "a%5Db",
		"%3B":          "%AZP253B",
		"plain [text]": "plain [text%5D",
	}
	for in, want := range tests {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNeutralize(t *testing.T) {
	if got := Neutralize("x ##vso[task.setvariable variable=a]1 ##vso[task.complete]"); got !=
		"x ##vso [task.setvariable variable=a]1 ##vso [task.complete]" {
		t.Errorf("Neutralize = %q", got)
	}
}

func TestCommand(t *testing.T) {
	a := github.Annotation{EndLine: 0, EndCol: 0, Fingerprint: "", Tool: "", RuleURL: "", Severity: "", Synthetic: false, Level: github.LevelError, File: "src/a;b.ts", Line: 3, Col: 7, Source: "eslint", Code: "no-var", Message: "Unexpected var\n100% ]"}
	want := "##vso[task.logissue type=error;sourcepath=src/a%3Bb.ts;linenumber=3;columnnumber=7;code=eslint(no-var)]Unexpected var%0A100%AZP25 %5D"
	if got := Command(a); got != want {
		t.Errorf("Command =\n%s\nwant\n%s", got, want)
	}
	noFile := github.Annotation{File: "", Line: 0, EndLine: 0, Col: 0, EndCol: 0, Code: "", Fingerprint: "", Tool: "", RuleURL: "", Severity: "", Level: github.LevelError, Source: "tsc", Message: "tsc exited 2 without parsable findings", Synthetic: true}
	if got := Command(noFile); got != "##vso[task.logissue type=error;code=tsc]tsc exited 2 without parsable findings" {
		t.Errorf("Command = %s", got)
	}
}

func finding(path string, row int, severity string) report.Finding {
	return report.Finding{
		FingerprintBasis: "",
		RuleURL:          "",
		Gates:            false,
		Baselined:        false,
		Provenance:       "",
		Fingerprint:      fmt.Sprintf("%s:%d:%s", path, row, severity), Tool: "eslint", Source: "eslint", Code: "r",
		Severity: severity, Reported: true, Shown: true, Kind: "issue", Message: "m",
		Location: report.Location{Col: 0, EndCol: 0, Unit: "", Bytes: nil, Utf16: nil, Path: path, Row: row, EndRow: row, Chars: &textpos.Span{Start: 1, End: 2}, Precision: "exact"},
	}
}

// TestSelect: ten issues of each type, info and hint left out, touched files
// first, and one plain line counting the rest.
func TestSelect(t *testing.T) {
	fs := make([]report.Finding, 0, 15)
	for i := range 12 {
		fs = append(fs, finding(fmt.Sprintf("f%02d.ts", i), 1, "error"))
	}
	fs = append(fs, finding("w.ts", 1, "warning"), finding("i.ts", 1, "info"), finding("h.ts", 1, "hint"))
	run := &report.Run{
		Schema:      "",
		Fingerprint: "",
		Datamitsu:   report.Producer{Version: "", Configuration: ""},
		StartedAt:   zerovalue_time.Time{},
		EndedAt:     zerovalue_time.Time{},
		Selection:   report.Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil},
		FailFast:    false,
		Complete:    false,
		Incomplete:  nil,
		Exports:     nil,
		CI:          report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, Operations: []report.Operation{{
			Ran:             false,
			Success:         false,
			Duration:        0,
			Skipped:         nil,
			Cancelled:       nil,
			ChangesObserved: false,
			ChangesReason:   "",
			ChangesDetail:   "",
			ChangesScope:    nil,
			Changes:         nil, Name: "lint", Tools: []report.ToolRun{{
				App:         report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
				Parser:      nil,
				FailOn:      "",
				GateActive:  false,
				Category:    "",
				Complete:    false,
				Incomplete:  nil,
				Name:        "eslint",
				Invocations: []report.Invocation{{ID: "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", Findings: fs}},
			}},
		}},
	}
	candidates := Candidates(run)
	if len(candidates) != 13 || !Overflows(candidates) {
		t.Fatalf("candidates = %d, want 12 errors and a warning, overflowing", len(candidates))
	}
	sel := Select(candidates, map[string]bool{"f11.ts": true})
	if len(sel.Issues) != 11 || sel.Omitted != 2 || sel.Issues[0].File != "f11.ts" || sel.Issues[10].Level != github.LevelWarning {
		t.Errorf("selection = %+v", sel)
	}
	var out bytes.Buffer
	if err := Print(&out, sel, []string{"out/run.json"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 12 || lines[11] != "datamitsu: 2 more findings in out/run.json" {
		t.Errorf("printed =\n%s", out.String())
	}
}

// TestRoundTrip: what the renderer prints, the embedded azure-logissue parser
// reads back as the same findings.
func TestRoundTrip(t *testing.T) {
	candidates := []github.Annotation{
		{EndLine: 0, EndCol: 0, Fingerprint: "", Tool: "", RuleURL: "", Severity: "", Synthetic: false, Level: github.LevelError, File: "src/a;b.ts", Line: 3, Col: 7, Source: "eslint", Code: "no-var", Message: "Unexpected var\n100% ] ##vso[task.complete]"},
		{EndLine: 0, Col: 0, EndCol: 0, Fingerprint: "", Tool: "", RuleURL: "", Severity: "", Synthetic: false, Level: github.LevelWarning, File: "b.py", Line: 9, Source: "ruff", Code: "W291", Message: "Trailing whitespace"},
	}
	var out bytes.Buffer
	if err := Print(&out, Select(candidates, nil), nil); err != nil {
		t.Fatal(err)
	}
	resp, err := parsermanager.ParseEmbedded(context.Background(), "azure-logissue", out.Bytes(), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Recognized || len(resp.Diagnostics) != 2 {
		t.Fatalf("parsed = %+v", resp)
	}
	d := resp.Diagnostics[0]
	if d.File == nil || *d.File != "src/a;b.ts" || d.Row == nil || *d.Row != 3 || d.Col == nil || *d.Col != 7 ||
		d.Code == nil || *d.Code != "eslint(no-var)" || d.Message != "Unexpected var\n100% ] ##vso [task.complete]" {
		t.Errorf("first diagnostic = %+v", d)
	}
	if w := resp.Diagnostics[1]; w.Severity == nil || w.Row == nil || *w.Row != 9 || w.Col != nil {
		t.Errorf("second diagnostic = %+v", w)
	}
}
