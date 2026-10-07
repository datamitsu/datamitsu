package codequality

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
	"github.com/datamitsu/datamitsu/internal/textpos"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaName is the resource testdata/schema/gitlab-code-quality.schema.json is
// compiled under; the file's $comment says where its rules come from.
const schemaName = "gitlab-code-quality.schema.json"

func schemaError(t *testing.T, doc []byte) error {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "schema", schemaName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	schemaDoc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaName, schemaDoc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(schemaName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("not JSON: %v\n%s", err, doc)
	}
	return schema.Validate(instance)
}

func TestSchemaRejects(t *testing.T) {
	for _, doc := range []string{
		`{}`,
		`[{"description": "d", "check_name": "c", "fingerprint": "f", "severity": "error", "location": {"path": "a", "lines": {"begin": 1}}}]`,
		`[{"description": "d", "check_name": "c", "fingerprint": "f", "severity": "minor", "location": {"path": "./a", "lines": {"begin": 1}}}]`,
		`[{"description": "d", "check_name": "c", "fingerprint": "f", "severity": "minor", "location": {"path": "a"}}]`,
	} {
		if schemaError(t, []byte(doc)) == nil {
			t.Errorf("the schema accepted %s", doc)
		}
	}
}

func finding(tool, code, path string, row int, severity string) report.Finding {
	return report.Finding{
		Fingerprint: report.Fingerprint(tool, code, path, report.RowHash(row), 0),
		Tool:        tool, Source: tool, Code: code, Severity: severity, Kind: "issue", Message: tool + " says <so>",
		Location: report.Location{Path: path, Row: row, EndRow: row, Chars: &textpos.Span{Start: 2, End: 5}, Precision: "exact"},
	}
}

func sampleRun() *report.Run {
	imprecise := finding("eslint", "no-var", "src/a.ts", 3, "hint")
	imprecise.Location = report.Location{Path: "src/a.ts", Row: 3, EndRow: 4, Precision: "unknown"}
	point := finding("eslint", "", "src/b.ts", 1, "info")
	point.Location.Chars = &textpos.Span{Start: 4, End: 4}
	return &report.Run{
		Selection: report.Selection{Mode: "all"},
		Exports:   []report.Export{{Format: "codequality", Path: "gl-code-quality.json", Status: "written"}},
		Operations: []report.Operation{
			{Name: "fix", Ran: true, Tools: []report.ToolRun{{Name: "prettier", Complete: true, Invocations: []report.Invocation{{
				Findings: []report.Finding{finding("prettier", "x", "a.md", 1, "error")},
			}}}}},
			{Name: "lint", Ran: true, Tools: []report.ToolRun{
				{Name: "eslint", Complete: true, Invocations: []report.Invocation{{Findings: []report.Finding{
					finding("eslint", "no-unused-vars", "src/a.ts", 1, "warning"), imprecise, point,
					finding("eslint", "no-var", "/elsewhere/c.ts", 2, "error"),
				}}}},
				{Name: "gitleaks", Category: "security", Complete: true, Invocations: []report.Invocation{{Findings: []report.Finding{
					func() report.Finding {
						f := finding("gitleaks", "aws-key", "keys.txt", 7, "error")
						f.Kind = "security"
						return f
					}(),
				}}}},
				{Name: "tsc", Incomplete: []report.Reason{"cancelled"}, Invocations: []report.Invocation{{Findings: []report.Finding{
					finding("tsc", "TS2322", "src/a.ts", 9, "error"),
					{Tool: "tsc", Source: "tsc", Severity: "error", Gates: true, Kind: "synthetic", Message: "tsc exited 2 without parsable findings", Location: report.Location{Precision: "unknown"}},
				}}}},
			}},
		},
	}
}

func render(t *testing.T, run *report.Run) []Issue {
	t.Helper()
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, run, nil); err != nil {
		t.Fatal(err)
	}
	if err := schemaError(t, b.Bytes()); err != nil {
		t.Fatalf("the report does not validate: %v\n%s", err, b.String())
	}
	var list []Issue
	if err := json.Unmarshal(b.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestRender(t *testing.T) {
	list := render(t, sampleRun())
	got := map[string]Issue{}
	fingerprints := map[string]bool{}
	for _, issue := range list {
		got[issue.CheckName+" "+issue.Location.Path] = issue
		if fingerprints[issue.Fingerprint] {
			t.Errorf("fingerprint %s is not unique", issue.Fingerprint)
		}
		fingerprints[issue.Fingerprint] = true
	}
	if len(list) != 5 {
		t.Fatalf("issues = %+v, want five: lint only, without the finding outside the repository or without a file", list)
	}
	unused := got["eslint/no-unused-vars src/a.ts"]
	want := Issue{
		Type: "issue", CheckName: "eslint/no-unused-vars", Description: "eslint says <so>", Categories: []string{"Style"}, Severity: "minor",
		Fingerprint: sampleRun().Operations[1].Tools[0].Invocations[0].Findings[0].Fingerprint,
		Location:    Location{Path: "src/a.ts", Positions: &Positions{Begin: Position{Line: 1, Column: 2}, End: Position{Line: 1, Column: 5}}},
	}
	if !reflect.DeepEqual(unused, want) {
		t.Errorf("issue = %+v\nwant %+v", unused, want)
	}
	if loc := got["eslint/no-var src/a.ts"].Location; loc.Positions != nil || *loc.Lines != (Lines{Begin: 3, End: 4}) {
		t.Errorf("a finding whose columns could not be converted = %+v, want lines", loc)
	}
	if p := got["eslint src/b.ts"].Location.Positions; p == nil || p.End != p.Begin {
		t.Errorf("a point = %+v", p)
	}
	if leak := got["gitleaks/aws-key keys.txt"]; leak.Severity != "major" || !reflect.DeepEqual(leak.Categories, []string{"Security"}) {
		t.Errorf("a security finding = %+v", leak)
	}
	if tsc := got["tsc/TS2322 src/a.ts"]; tsc.Severity != "major" {
		t.Errorf("an incomplete tool's finding is kept: %+v", tsc)
	}
}

func TestSeverity(t *testing.T) {
	for severity, want := range map[string]string{"error": "major", "warning": "minor", "info": "info", "hint": "info"} {
		if got := Severity(severity); got != want {
			t.Errorf("Severity(%s) = %s, want %s", severity, got, want)
		}
	}
}

func TestCompanion(t *testing.T) {
	c := (Renderer{}).Companion(sampleRun(), nil)
	wantOmitted := []common.OmittedFindings{
		{Tool: "eslint", Reason: "outside-repository", Count: 1},
		{Tool: "tsc", Reason: "no-file", Count: 1},
	}
	if !reflect.DeepEqual(c.Omitted, wantOmitted) {
		t.Errorf("omitted = %+v, want %+v", c.Omitted, wantOmitted)
	}
	if c.Complete || len(c.Tools) != 3 || c.Tools[2].Name != "tsc" || c.Tools[2].Incomplete[0] != "cancelled" {
		t.Errorf("companion = %+v, want the lint tools with tsc incomplete", c)
	}
}

func TestEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, &report.Run{}, nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != "[]\n" {
		t.Errorf("an empty report = %q, want an empty array", b.String())
	}
}
