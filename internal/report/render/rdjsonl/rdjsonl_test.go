package rdjsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	zerovalue_time "time"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaSource is where testdata/schema/Diagnostic.json was copied from, byte
// for byte: the JSON Schema reviewdog generates from proto/rdf/reviewdog.proto,
// at release v0.21.2.
const schemaSource = "https://github.com/reviewdog/reviewdog/blob/v0.21.2/proto/rdf/jsonschema/Diagnostic.json"

func compile(t *testing.T) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "schema", "Diagnostic.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaSource, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(schemaSource)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestSchemaRejects(t *testing.T) {
	schema := compile(t)
	for _, line := range []string{`{"severity": "FATAL"}`, `{"message": 3}`, `{"location": {"range": {"start": {"line": "x"}}}}`} {
		instance, err := jsonschema.UnmarshalJSON(strings.NewReader(line))
		if err != nil {
			t.Fatal(err)
		}
		if schema.Validate(instance) == nil {
			t.Errorf("the schema accepted %s", line)
		}
	}
}

func sampleRun() *report.Run {
	// "é" is two UTF-8 bytes: a column after it counts one more byte than
	// characters.
	accented := report.Finding{
		Fingerprint:      "",
		FingerprintBasis: "",
		Reported:         false,
		Gates:            false,
		Shown:            false,
		Baselined:        false,
		Provenance:       "",
		Tool:             "eslint", Source: "eslint", Code: "no-var", RuleURL: "https://eslint.org/docs/latest/rules/no-var", Severity: "error", Kind: "issue", Message: "Unexpected var",
		Location: report.Location{Col: 0, EndCol: 0, Unit: "", Utf16: nil, Path: "src/a.ts", Row: 2, EndRow: 2, Chars: &textpos.Span{Start: 5, End: 8}, Bytes: &textpos.Span{Start: 6, End: 9}, Precision: "exact"},
	}
	imprecise := report.Finding{
		Fingerprint:      "",
		FingerprintBasis: "",
		Code:             "",
		RuleURL:          "",
		Reported:         false,
		Gates:            false,
		Shown:            false,
		Baselined:        false,
		Provenance:       "",
		Tool:             "eslint", Source: "eslint", Severity: "hint", Kind: "issue", Message: "no rule",
		Location: report.Location{Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Path: "src/a.ts", Row: 3, EndRow: 5, Col: 2, EndCol: 4, Precision: "unknown"},
	}
	point := report.Finding{
		Fingerprint:      "",
		FingerprintBasis: "",
		RuleURL:          "",
		Reported:         false,
		Gates:            false,
		Shown:            false,
		Baselined:        false,
		Provenance:       "",
		Tool:             "eslint", Source: "eslint", Code: "eqeqeq", Severity: "warning", Kind: "issue", Message: "use ===",
		Location: report.Location{Col: 0, EndCol: 0, Unit: "", Utf16: nil, Path: "/elsewhere/b.ts", Row: 1, EndRow: 1, Chars: &textpos.Span{Start: 4, End: 4}, Bytes: &textpos.Span{Start: 4, End: 4}, Precision: "ascii"},
	}
	synthetic := report.Finding{
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
	}
	return &report.Run{
		RedactedReasons: nil,

		Fingerprint: "",
		Datamitsu:   report.Producer{Version: "", Configuration: ""},
		StartedAt:   zerovalue_time.Time{},
		EndedAt:     zerovalue_time.Time{},
		FailFast:    false,
		Complete:    false,
		Incomplete:  nil,
		Exports:     nil,
		CI:          report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Selection:   report.Selection{Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil, Mode: "all"},
		Operations: []report.Operation{
			{Success: false, Duration: 0, Skipped: nil, Cancelled: nil, ChangesObserved: false, ChangesReason: "", ChangesDetail: "", ChangesScope: nil, Changes: nil, Name: "fix", Ran: true, Tools: []report.ToolRun{{
				RedactedReasons: nil,
				App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Incomplete: nil, Name: "prettier", Invocations: []report.Invocation{{
					RedactedState:      0,
					RedactedExtraction: 0,
					ID:                 "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", Findings: []report.Finding{accented},
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
						Parser:          nil, FailOn: "", GateActive: false, Category: "", Incomplete: nil, Name: "eslint", Complete: true, App: report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: "https://eslint.org"}, Invocations: []report.Invocation{{
							RedactedState:      0,
							RedactedExtraction: 0,
							ID:                 "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", Findings: []report.Finding{imprecise, accented, point},
						}},
					},
					{
						RedactedReasons: nil,
						App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Name: "tsc", Incomplete: []report.Reason{"no-extraction"}, Invocations: []report.Invocation{{
							RedactedState:      0,
							RedactedExtraction: 0,
							ID:                 "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", Findings: []report.Finding{synthetic},
						}},
					},
				},
			},
		},
	}
}

func TestRender(t *testing.T) {
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	schema := compile(t)
	var got []Diagnostic
	scanner := bufio.NewScanner(&b)
	for scanner.Scan() {
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(scanner.Bytes()))
		if err != nil {
			t.Fatalf("line is not JSON: %v\n%s", err, scanner.Text())
		}
		if err := schema.Validate(instance); err != nil {
			t.Errorf("line does not validate: %v\n%s", err, scanner.Text())
		}
		var d Diagnostic
		if err := json.Unmarshal(scanner.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		got = append(got, d)
	}
	want := []Diagnostic{
		{
			Message: "use ===", Severity: "WARNING", Source: Source{Name: "eslint", URL: "https://eslint.org"}, Code: &Code{URL: "", Value: "eqeqeq"},
			Location: &Location{Path: "/elsewhere/b.ts", Range: &Range{End: nil, Start: Position{Line: 1, Column: 4}}},
		},
		{
			Message: "Unexpected var", Severity: "ERROR", Source: Source{Name: "eslint", URL: "https://eslint.org"},
			Code:     &Code{Value: "no-var", URL: "https://eslint.org/docs/latest/rules/no-var"},
			Location: &Location{Path: "src/a.ts", Range: &Range{Start: Position{Line: 2, Column: 6}, End: &Position{Line: 2, Column: 9}}},
		},
		{
			Code:    nil,
			Message: "no rule", Severity: "INFO", Source: Source{Name: "eslint", URL: "https://eslint.org"},
			Location: &Location{Path: "src/a.ts", Range: &Range{Start: Position{Column: 0, Line: 3}, End: &Position{Column: 0, Line: 5}}},
		},
		{Location: nil, Code: nil, Message: "tsc exited 2 without parsable findings", Severity: "ERROR", Source: Source{URL: "", Name: "tsc"}},
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("diagnostics =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
	if strings.Contains(b.String(), "suggestions") || strings.Contains(b.String(), "original_output") {
		t.Error("the stream carries suggestions or the tool's output")
	}
}

func TestCompanion(t *testing.T) {
	c := (Renderer{}).Companion(sampleRun(), nil)
	if c.Format != "rdjsonl" || c.Complete || len(c.Tools) != 2 || c.Tools[1].Name != "tsc" {
		t.Errorf("companion = %+v", c)
	}
}

func TestSeverity(t *testing.T) {
	for severity, want := range map[string]string{"error": "ERROR", "warning": "WARNING", "info": "INFO", "hint": "INFO"} {
		if got := Severity(severity); got != want {
			t.Errorf("Severity(%s) = %s, want %s", severity, got, want)
		}
	}
}
