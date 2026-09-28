package checkstyle

import (
	"bytes"
	"encoding/xml"
	"reflect"
	"strings"
	"testing"

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
		Tool: "eslint", Source: "eslint", Code: code, Severity: severity, Kind: "issue", Message: msg,
		Location: report.Location{Path: path, Row: row, EndRow: row, Chars: &textpos.Span{Start: 7, End: 9}, Precision: "exact"},
	}
}

func TestRender(t *testing.T) {
	imprecise := finding("src/a.ts", 9, "hint", "", "no rule")
	imprecise.Location.Chars, imprecise.Location.Precision = nil, "unknown"
	run := &report.Run{
		Datamitsu: report.Producer{Version: "1.2.3"},
		Operations: []report.Operation{
			{Name: "fix", Ran: true, Tools: []report.ToolRun{{Name: "prettier", Invocations: []report.Invocation{{Findings: []report.Finding{finding("x.md", 1, "error", "c", "fix")}}}}}},
			{Name: "lint", Ran: true, Tools: []report.ToolRun{
				{Name: "eslint", Complete: true, Invocations: []report.Invocation{{Findings: []report.Finding{
					imprecise, finding("src/a.ts", 3, "error", "no-var", `say "<no>" & go`), finding("/elsewhere/b.ts", 1, "info", "x", "outside"),
				}}}},
				{Name: "tsc", Incomplete: []report.Reason{"no-extraction"}, Invocations: []report.Invocation{{Findings: []report.Finding{{
					Tool: "tsc", Source: "tsc", Severity: "error", Gates: true, Kind: "synthetic",
					Message: "tsc exited 2 without parsable findings", Location: report.Location{Precision: "unknown"},
				}}}}},
			}},
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
		XMLName: xml.Name{Local: "checkstyle"},
		Version: "datamitsu-1.2.3",
		Files: []xmlFile{
			{Name: "/elsewhere/b.ts", Errors: []xmlError{{Line: "1", Column: "7", Severity: "info", Message: "outside", Source: "eslint/x"}}},
			{Name: "src/a.ts", Errors: []xmlError{
				{Line: "3", Column: "7", Severity: "error", Message: `say "<no>" & go`, Source: "eslint/no-var"},
				{Line: "9", Severity: "info", Message: "no rule", Source: "eslint"},
			}},
			{Name: "", Errors: []xmlError{{Severity: "error", Message: "tsc exited 2 without parsable findings", Source: "tsc"}}},
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
	if err := (Renderer{}).Render(&b, &report.Run{}, nil); err != nil {
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
