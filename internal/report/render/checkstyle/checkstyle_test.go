package checkstyle

import (
	"bytes"
	"encoding/xml"
	"reflect"
	"strings"
	"testing"
	zerovalue_time "time"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

type xmlCheckstyle struct {
	XMLName xml.Name  `xml:"checkstyle"`
	Version string    `xml:"version,attr"`
	Files   []xmlFile `xml:"file"`
}

type xmlFile struct {
	Name   string     `xml:"name,attr"`
	Errors []xmlError `xml:"error"`
}

type xmlError struct {
	Line     string `xml:"line,attr"`
	Column   string `xml:"column,attr"`
	Severity string `xml:"severity,attr"`
	Message  string `xml:"message,attr"`
	Source   string `xml:"source,attr"`
}

func finding(path string, row int, severity, code, msg string) report.Finding {
	return report.Finding{
		Fingerprint:      "",
		FingerprintBasis: "",
		RuleURL:          "",
		Reported:         false,
		Gates:            false,
		Shown:            false,
		Baselined:        false,
		Provenance:       "",
		Tool:             "eslint", Source: "eslint", Code: code, Severity: severity, Kind: "issue", Message: msg,
		Location: report.Location{Col: 0, EndCol: 0, Unit: "", Bytes: nil, Utf16: nil, Path: path, Row: row, EndRow: row, Chars: &textpos.Span{Start: 7, End: 9}, Precision: "exact"},
	}
}

func TestRender(t *testing.T) {
	imprecise := finding("src/a.ts", 9, "hint", "", "no rule")
	imprecise.Location.Chars, imprecise.Location.Precision = nil, "unknown"
	run := &report.Run{
		RedactedReasons: nil,

		Fingerprint: "",
		StartedAt:   zerovalue_time.Time{},
		EndedAt:     zerovalue_time.Time{},
		Selection:   report.Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil},
		FailFast:    false,
		Complete:    false,
		Incomplete:  nil,
		Exports:     nil,
		CI:          report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Datamitsu:   report.Producer{Configuration: "", Version: "1.2.3"},
		Operations: []report.Operation{
			{Success: false, Duration: 0, Skipped: nil, Cancelled: nil, ChangesObserved: false, ChangesReason: "", ChangesDetail: "", ChangesScope: nil, Changes: nil, Name: "fix", Ran: true, Tools: []report.ToolRun{{
				RedactedReasons: nil,
				App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Incomplete: nil, Name: "prettier", Invocations: []report.Invocation{{
					RedactedState:      0,
					RedactedExtraction: 0,
					ID:                 "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", Findings: []report.Finding{finding("x.md", 1, "error", "c", "fix")},
				}},
			}}},
			{
				Success:         false,
				Duration:        0,
				Skipped:         nil,
				Cancelled:       nil,
				ChangesObserved: false,
				ChangesReason:   "",
				ChangesDetail:   "",
				ChangesScope:    nil,
				Changes:         nil, Name: "lint", Ran: true, Tools: []report.ToolRun{
					{
						RedactedReasons: nil,

						App:        report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
						Parser:     nil,
						FailOn:     "",
						GateActive: false,
						Category:   "",
						Incomplete: nil, Name: "eslint", Complete: true, Invocations: []report.Invocation{{
							RedactedState:      0,
							RedactedExtraction: 0,

							ID:          "",
							TaskID:      "",
							Dir:         "",
							Scope:       "",
							Granularity: "",
							Arity:       "",
							Coverage:    "",
							WholeUnit:   false,
							State:       "",
							ExitCode:    nil,
							Success:     false,
							FailureKind: "",
							Duration:    0,
							Extraction:  "",
							Provenance:  "",
							Files:       nil,
							Step:        0,
							Changes:     nil,
							OutputTail:  "", Findings: []report.Finding{
								imprecise, finding("src/a.ts", 3, "error", "no-var", `say "<no>" & go`), finding("/elsewhere/b.ts", 1, "info", "x", "outside"),
							},
						}},
					},
					{
						RedactedReasons: nil,

						App:        report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
						Parser:     nil,
						FailOn:     "",
						GateActive: false,
						Category:   "",
						Complete:   false, Name: "tsc", Incomplete: []report.Reason{"no-extraction"}, Invocations: []report.Invocation{{
							RedactedState:      0,
							RedactedExtraction: 0,

							ID:          "",
							TaskID:      "",
							Dir:         "",
							Scope:       "",
							Granularity: "",
							Arity:       "",
							Coverage:    "",
							WholeUnit:   false,
							State:       "",
							ExitCode:    nil,
							Success:     false,
							FailureKind: "",
							Duration:    0,
							Extraction:  "",
							Provenance:  "",
							Files:       nil,
							Step:        0,
							Changes:     nil,
							OutputTail:  "", Findings: []report.Finding{{
								Fingerprint:      "",
								FingerprintBasis: "",
								Code:             "",
								RuleURL:          "",
								Reported:         false,
								Shown:            false,
								Baselined:        false,
								Provenance:       "",
								Tool:             "tsc", Source: "tsc", Severity: "error", Gates: true, Kind: "synthetic",
								Message: "tsc exited 2 without parsable findings", Location: report.Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: "unknown"},
							}},
						}},
					},
				},
			},
		},
	}
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, run, nil); err != nil {
		t.Fatal(err)
	}
	var doc xmlCheckstyle
	if err := xml.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("not well-formed XML: %v\n%s", err, b.String())
	}
	want := xmlCheckstyle{
		XMLName: xml.Name{Space: "", Local: "checkstyle"},
		Version: "datamitsu-1.2.3",
		Files: []xmlFile{
			{Name: "/elsewhere/b.ts", Errors: []xmlError{{Line: "1", Column: "7", Severity: "info", Message: "outside", Source: "eslint/x"}}},
			{Name: "src/a.ts", Errors: []xmlError{
				{Line: "3", Column: "7", Severity: "error", Message: `say "<no>" & go`, Source: "eslint/no-var"},
				{Column: "", Line: "9", Severity: "info", Message: "no rule", Source: "eslint"},
			}},
			{Name: "", Errors: []xmlError{{Line: "", Column: "", Severity: "error", Message: "tsc exited 2 without parsable findings", Source: "tsc"}}},
		},
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("document = %+v\nwant %+v\n%s", doc, want, b.String())
	}
	if !strings.HasPrefix(b.String(), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("no XML declaration:\n%s", b.String())
	}

	c := (Renderer{}).Companion(run, nil)
	if c.Format != "checkstyle" || c.Complete || len(c.Tools) != 2 {
		t.Errorf("companion = %+v, want the lint tools with tsc incomplete", c)
	}
}

func TestEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, &report.Run{
		RedactedReasons: nil,

		Fingerprint: "", Datamitsu: report.Producer{Version: "", Configuration: ""}, StartedAt: zerovalue_time.Time{}, EndedAt: zerovalue_time.Time{}, Selection: report.Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil}, FailFast: false, Complete: false, Incomplete: nil, Operations: nil, Exports: nil, CI: report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if want := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<checkstyle version="datamitsu-">` + "\n</checkstyle>\n"; b.String() != want {
		t.Errorf("empty document = %q, want %q", b.String(), want)
	}
}

func TestSeverity(t *testing.T) {
	for severity, want := range map[string]string{"error": "error", "warning": "warning", "info": "info", "hint": "info"} {
		if got := Severity(severity); got != want {
			t.Errorf("Severity(%s) = %s, want %s", severity, got, want)
		}
	}
}
