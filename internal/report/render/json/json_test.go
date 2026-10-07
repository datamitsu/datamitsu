package json

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/report"
)

func sampleRun() *report.Run {
	code := 1
	return &report.Run{
		Schema:    report.SchemaVersion,
		Datamitsu: report.Producer{Version: "1.2.3", Configuration: "datamitsu.config"},
		StartedAt: time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC),
		EndedAt:   time.Date(2023, 11, 14, 22, 13, 21, 0, time.UTC),
		Selection: report.Selection{Mode: "all"},
		Operations: []report.Operation{{
			Name: "lint", Ran: true, Skipped: []report.Skip{}, Cancelled: []report.Cancel{},
			Tools: []report.ToolRun{{
				Name: "eslint", App: report.AppRef{Name: "eslint", Kind: "node", Version: "9.0.0"},
				Invocations: []report.Invocation{{
					ID: "eslint::1#1", State: "ran", ExitCode: &code, FailureKind: "exit",
					Files: []report.FileResult{{Path: "a.ts", State: "ran", ExitCode: &code}},
					Findings: []report.Finding{{
						Tool: "eslint", Source: "eslint", Code: "no-var", Severity: "error",
						Message: "Unexpected var <T> & more", Location: report.Location{Path: "a.ts", Row: 1, EndRow: 1, Col: 1, EndCol: 4},
					}},
				}},
			}},
		}},
		Exports: []report.Export{{Format: "json", Path: "-", Status: report.ExportWritten}},
	}
}

// The document decodes back into the model it came from and re-encodes byte
// for byte: `report render --format json` reproduces what the run wrote.
func TestRoundTrip(t *testing.T) {
	var first bytes.Buffer
	if err := (Renderer{}).Render(&first, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.String(), "{\n  \"schema\": \"datamitsu.report/1\",\n") || !strings.HasSuffix(first.String(), "}\n") {
		t.Errorf("not the two-space indented document with a trailing newline:\n%s", first.String())
	}
	run, err := Decode(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := (Renderer{}).Render(&second, run, nil); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Errorf("round trip changed the document:\n--- first\n%s\n--- second\n%s", first.String(), second.String())
	}
}

func TestDecodeRefusesOtherSchemas(t *testing.T) {
	for _, doc := range []string{
		`{"schema": "datamitsu.report/2"}`, `{}`, `not json`,
		`{"schema": "datamitsu.report/1"}{"schema": "datamitsu.report/1"}`,
		`{"schema": "datamitsu.report/1"} trailing`,
	} {
		if _, err := Decode(strings.NewReader(doc)); err == nil {
			t.Errorf("Decode(%s) accepted it", doc)
		}
	}
}
