package sarif

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
	"github.com/datamitsu/datamitsu/internal/textpos"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaSource is where testdata/schema/sarif-schema-2.1.0.json was copied
// from, byte for byte: the OASIS SARIF 2.1.0 schema (errata 01) at a recorded
// commit of the specification repository.
const schemaSource = "https://github.com/oasis-tcs/sarif-spec/blob/adbb670c018335b0f384e6dd8819f4ea055d7ee1/sarif-2.1/schema/sarif-schema-2.1.0.json"

// validate checks a document against the vendored SARIF schema.
func validate(t *testing.T, doc []byte) {
	t.Helper()
	if err := schemaError(t, doc); err != nil {
		t.Fatalf("the document does not validate against the SARIF 2.1.0 schema: %v\n%s", err, doc)
	}
}

func schemaError(t *testing.T, doc []byte) error {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "schema", "sarif-schema-2.1.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	schemaDoc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaSource, schemaDoc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(schemaSource)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("not JSON: %v\n%s", err, doc)
	}
	return schema.Validate(instance)
}

// The schema is live: a document it must reject is rejected.
func TestSchemaRejects(t *testing.T) {
	for _, doc := range []string{
		`{"version": "2.0.0", "runs": []}`,
		`{"version": "2.1.0", "runs": [{"tool": {"driver": {}}}]}`,
		`{"version": "2.1.0", "runs": [{"tool": {"driver": {"name": "x"}}, "results": [{"message": {"text": "m"}, "level": "fatal"}]}]}`,
	} {
		if schemaError(t, []byte(doc)) == nil {
			t.Errorf("the schema accepted %s", doc)
		}
	}
}

func span(start, end int) *textpos.Span { return &textpos.Span{Start: start, End: end} }

func finding(tool, code, path string, row int, severity, msg string) report.Finding {
	return report.Finding{
		Fingerprint: report.Fingerprint(tool, code, path, report.RowHash(row), 0),
		Tool:        tool, Source: tool, Code: code, Severity: severity, Kind: "issue", Message: msg, Provenance: "parser",
		Location: report.Location{Path: path, Row: row, EndRow: row, Col: 5, EndCol: 9, Chars: span(5, 9), Bytes: span(5, 9), Utf16: span(5, 9), Precision: "exact"},
	}
}

func sampleRun() *report.Run {
	one, zero := 1, 0
	eslint := finding("eslint", "no-unused-vars", "src/a.ts", 12, "warning", "'x' is <unused>")
	eslint.RuleURL = "https://eslint.org/docs/latest/rules/no-unused-vars"
	eslint.Gates = true
	noRule := finding("eslint", "", "src/a.ts", 3, "hint", "no rule")
	noRule.Location = report.Location{Path: "src/a.ts", Row: 3, EndRow: 5, Precision: "unknown"}
	outside := finding("eslint", "no-var", "/home/me/other repo/b.ts", 1, "error", "outside")
	outside.Location.Chars, outside.Location.Precision = nil, "unknown"
	noFile := report.Finding{Tool: "eslint", Source: "eslint", Code: "config", Severity: "info", Kind: "issue", Message: "no file", Location: report.Location{Precision: "unknown"}}
	return &report.Run{
		Schema:    report.SchemaVersion,
		Selection: report.Selection{Mode: "all"},
		Operations: []report.Operation{
			{Name: "fix", Ran: true, Tools: []report.ToolRun{{Name: "prettier", Complete: true}}},
			{Name: "lint", Ran: true, Tools: []report.ToolRun{
				{
					Name: "eslint", Complete: true,
					App: report.AppRef{Name: "eslint", Kind: "node", Version: "9.12.0", OfficialURL: "https://www.npmjs.com/package/eslint"},
					Invocations: []report.Invocation{
						{
							ID: "eslint:packages/api:1#1", Dir: "packages/api", State: "ran", ExitCode: &one, Extraction: "parsed-findings",
							Findings: []report.Finding{eslint, noRule, outside, noFile},
						},
						{ID: "eslint::2#cached", State: "cached", Success: true, Extraction: "parsed-clean"},
					},
				},
				{
					Name: "tsc", Complete: true, App: report.AppRef{Name: "tsc", Kind: "node"},
					Invocations: []report.Invocation{{ID: "tsc::3#1", State: "ran", ExitCode: &one, Extraction: "parsed-clean", Findings: []report.Finding{{
						Tool: "tsc", Source: "tsc", Severity: "error", Gates: true, Kind: "synthetic",
						Message: "tsc exited 1 without parsable findings", Location: report.Location{Precision: "unknown"},
					}}}},
				},
				{
					Name: "hadolint", Incomplete: []report.Reason{"cancelled"},
					Invocations: []report.Invocation{{ID: "hadolint::4#1", State: "cancelled", ExitCode: &zero}},
				},
			}},
		},
	}
}

func render(t *testing.T, run *report.Run, options map[string]string) (Log, []byte) {
	t.Helper()
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, run, options); err != nil {
		t.Fatal(err)
	}
	validate(t, b.Bytes())
	var log Log
	if err := json.Unmarshal(b.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	return log, b.Bytes()
}

func TestRender(t *testing.T) {
	log, raw := render(t, sampleRun(), nil)
	if log.Version != "2.1.0" || log.Schema != SchemaURI {
		t.Errorf("version, $schema = %q, %q", log.Version, log.Schema)
	}
	if len(log.Runs) != 2 || log.Runs[0].Tool.Driver.Name != "eslint" || log.Runs[1].Tool.Driver.Name != "tsc" {
		t.Fatalf("runs = %+v, want eslint and tsc: the lint operation, one run per complete tool", log.Runs)
	}
	eslint := log.Runs[0]
	if eslint.AutomationDetails.ID != "datamitsu/" || eslint.ColumnKind != "unicodeCodePoints" {
		t.Errorf("automationDetails.id, columnKind = %q, %q", eslint.AutomationDetails.ID, eslint.ColumnKind)
	}
	if d := eslint.Tool.Driver; d.Version != "9.12.0" || d.InformationURI != "https://www.npmjs.com/package/eslint" {
		t.Errorf("driver = %+v", d)
	}
	wantRules := []Rule{
		{ID: "eslint/unknown", ShortDescription: Message{Text: "eslint/unknown"}},
		{ID: "no-unused-vars", ShortDescription: Message{Text: "no-unused-vars"}, HelpURI: "https://eslint.org/docs/latest/rules/no-unused-vars"},
		{ID: "no-var", ShortDescription: Message{Text: "no-var"}},
	}
	if !reflect.DeepEqual(eslint.Tool.Driver.Rules, wantRules) {
		t.Errorf("rules = %+v, want %+v", eslint.Tool.Driver.Rules, wantRules)
	}
	if len(eslint.Results) != 3 {
		t.Fatalf("results = %+v, want three: a finding without a file is a notification", eslint.Results)
	}
	outside, noRule, unused := eslint.Results[0], eslint.Results[1], eslint.Results[2]
	if got := outside.Locations[0].PhysicalLocation.ArtifactLocation; got != (ArtifactLocation{URI: "file:///home/me/other%20repo/b.ts"}) {
		t.Errorf("outside-root location = %+v, want a file URI without a base", got)
	}
	if outside.Locations[0].PhysicalLocation.Region.StartColumn != 0 {
		t.Errorf("an imprecise column was written: %+v", outside.Locations[0].PhysicalLocation.Region)
	}
	if noRule.RuleID != "eslint/unknown" || noRule.RuleIndex != 0 || noRule.Level != "note" {
		t.Errorf("result without a rule = %+v", noRule)
	}
	if r := noRule.Locations[0].PhysicalLocation.Region; *r != (Region{StartLine: 3, EndLine: 5}) {
		t.Errorf("multi-line region without columns = %+v", r)
	}
	want := sampleRun().Operations[1].Tools[0].Invocations[0].Findings[0].Fingerprint
	if unused.PartialFingerprints["primaryLocationLineHash"] != want || unused.PartialFingerprints["datamitsu/v1"] != want {
		t.Errorf("partialFingerprints = %v, want the finding's fingerprint %s under both keys", unused.PartialFingerprints, want)
	}
	if unused.RuleIndex != 1 || unused.Level != "warning" || !unused.Properties.Gates || unused.Properties.Provenance != "parser" {
		t.Errorf("result = %+v", unused)
	}
	if got := unused.Locations[0].PhysicalLocation; got.ArtifactLocation != (ArtifactLocation{URI: "src/a.ts", URIBaseID: "%SRCROOT%"}) ||
		*got.Region != (Region{StartLine: 12, StartColumn: 5, EndLine: 12, EndColumn: 9}) {
		t.Errorf("location = %+v %+v", got.ArtifactLocation, got.Region)
	}
	if !strings.Contains(string(raw), `"'x' is <unused>"`) {
		t.Errorf("messages are written without HTML escapes:\n%s", raw)
	}

	inv := eslint.Invocations[0]
	if !inv.ExecutionSuccessful || *inv.ExitCode != 1 || *inv.WorkingDirectory != (ArtifactLocation{URI: "packages/api/", URIBaseID: "%SRCROOT%"}) ||
		inv.Properties.Datamitsu != (InvocationFacts{ID: "eslint:packages/api:1#1", State: "ran", Extraction: "parsed-findings"}) {
		t.Errorf("invocation = %+v", inv)
	}
	if want := []Notification{{Level: "note", Message: Message{Text: "no file"}, Descriptor: &Descriptor{ID: "config"}}}; !reflect.DeepEqual(inv.ToolExecutionNotifications, want) {
		t.Errorf("notifications = %+v, want %+v", inv.ToolExecutionNotifications, want)
	}
	if cached := eslint.Invocations[1]; !cached.ExecutionSuccessful || cached.ExitCode != nil || cached.WorkingDirectory.URI != "./" {
		t.Errorf("cached invocation = %+v", cached)
	}

	tsc := log.Runs[1]
	if tsc.Invocations[0].ExecutionSuccessful || len(tsc.Results) != 0 || len(tsc.Invocations[0].ToolExecutionNotifications) != 1 ||
		tsc.Invocations[0].ToolExecutionNotifications[0] != (Notification{Level: "error", Message: Message{Text: "tsc exited 1 without parsable findings"}}) {
		t.Errorf("a tool that failed without a parsable finding = %+v", tsc)
	}
}

func TestOmitted(t *testing.T) {
	got := (Renderer{}).Omitted(sampleRun(), nil)
	want := []report.OmittedTool{{Operation: "lint", Tool: "hadolint", Reasons: []string{"cancelled"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Omitted = %+v, want %+v", got, want)
	}
}

func TestFixOnlyRun(t *testing.T) {
	run := sampleRun()
	run.Operations = run.Operations[:1]
	log, _ := render(t, run, nil)
	if len(log.Runs) != 1 || log.Runs[0].Tool.Driver.Name != "prettier" {
		t.Errorf("runs = %+v, want the fix operation's tool", log.Runs)
	}
}

func TestCategory(t *testing.T) {
	for _, tc := range []struct{ option, want string }{
		{"", "datamitsu/"}, {"lint-linux", "lint-linux/"}, {"lint-linux/", "lint-linux/"}, {"a/b", "a/b/"},
	} {
		options := map[string]string{}
		if tc.option != "" {
			options["category"] = tc.option
		}
		log, _ := render(t, sampleRun(), options)
		for _, r := range log.Runs {
			if r.AutomationDetails.ID != tc.want {
				t.Errorf("category %q: automationDetails.id = %q, want %q", tc.option, r.AutomationDetails.ID, tc.want)
			}
		}
	}
	for _, bad := range []string{"", "/", " "} {
		if err := (Renderer{}).CheckOptions(map[string]string{"category": bad}); err == nil {
			t.Errorf("category %q was accepted", bad)
		}
	}
}

func manyTools(n int) *report.Run {
	run := &report.Run{Selection: report.Selection{Mode: "all"}, Operations: []report.Operation{{Name: "lint", Ran: true}}}
	for i := range n {
		name := fmt.Sprintf("tool%02d", n-i)
		run.Operations[0].Tools = append(run.Operations[0].Tools, report.ToolRun{
			Name: name, Complete: true,
			Invocations: []report.Invocation{{ID: name + "::1#1", State: "ran", Success: true, Findings: []report.Finding{finding(name, "R1", "a.go", 1, "error", "m")}}},
		})
	}
	return run
}

// A run of more tools than one file holds is refused as one file and split
// over files of twenty in a directory, tools sorted by name, whatever order
// the run lists them in.
func TestSplit(t *testing.T) {
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, manyTools(21), nil); err == nil {
		t.Fatal("21 tools were written into one file")
	}
	files, err := (Renderer{}).RenderFiles(manyTools(21), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "datamitsu-1.sarif" || files[1].Name != "datamitsu-2.sarif" {
		t.Fatalf("files = %v", names(files))
	}
	tools := make([][]string, 0, len(files))
	for _, f := range files {
		validate(t, f.Data)
		var log Log
		if err := json.Unmarshal(f.Data, &log); err != nil {
			t.Fatal(err)
		}
		in := make([]string, 0, len(log.Runs))
		for _, r := range log.Runs {
			in = append(in, r.Tool.Driver.Name)
			if r.AutomationDetails.ID != "datamitsu/" {
				t.Errorf("%s: %s is in category %q; every file of a run shares one", f.Name, r.Tool.Driver.Name, r.AutomationDetails.ID)
			}
		}
		tools = append(tools, in)
	}
	if len(tools[0]) != 20 || tools[0][0] != "tool01" || tools[0][19] != "tool20" || !reflect.DeepEqual(tools[1], []string{"tool21"}) {
		t.Errorf("split = %v", tools)
	}

	empty, err := (Renderer{}).RenderFiles(&report.Run{}, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("a run without a tool to write = %v, %v; want no file", names(empty), err)
	}
	if why := (Renderer{}).Declines(&report.Run{}, nil); why == "" {
		t.Error("a run without a tool to write is not declined: code scanning refuses a SARIF file without a run")
	}
	if why := (Renderer{}).Declines(manyTools(1), nil); why != "" {
		t.Errorf("a run with a tool is declined: %s", why)
	}

	for name, want := range map[string]bool{"datamitsu-1.sarif": true, "datamitsu-12.sarif": true, "datamitsu-0.sarif": false, "other.sarif": false, "datamitsu-1.sarif.tmp": false} {
		if (Renderer{}).Owns(name) != want {
			t.Errorf("Owns(%q) = %v, want %v", name, !want, want)
		}
	}
}

func names(files []common.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

// A tool with more results than GitHub keeps from one run is left out rather
// than cut: GitHub would close the alerts of every result it dropped.
func TestTooManyResults(t *testing.T) {
	run := manyTools(2)
	tr := &run.Operations[0].Tools[0]
	for i := range ResultsPerRun {
		tr.Invocations[0].Findings = append(tr.Invocations[0].Findings, finding(tr.Name, "R2", "a.go", i+2, "warning", "m"))
	}
	log, _ := render(t, run, nil)
	if len(log.Runs) != 1 || log.Runs[0].Tool.Driver.Name != "tool01" {
		t.Errorf("runs = %d, want tool01 alone", len(log.Runs))
	}
	want := []report.OmittedTool{{Operation: "lint", Tool: "tool02", Reasons: []string{"too-many-results"}}}
	if got := (Renderer{}).Omitted(run, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("Omitted = %+v, want %+v", got, want)
	}
}

// A character outside the Basic Multilingual Plane before a finding is one
// code point: the column SARIF gets is the chars span, whatever unit the
// tool counted in.
func TestCodePointColumns(t *testing.T) {
	line := []byte("const s = \"😀\"; let x = 1;")
	// The tool counted UTF-16 units: the emoji is two of them.
	chars, bytesSpan, utf16, precision := textpos.Spans(line, line, 20, 21, textpos.UTF16)
	f := finding("eslint", "no-var", "a.ts", 1, "error", "m")
	f.Location.Chars, f.Location.Bytes, f.Location.Utf16, f.Location.Precision = &chars, &bytesSpan, &utf16, string(precision)
	run := manyTools(1)
	run.Operations[0].Tools[0].Invocations[0].Findings = []report.Finding{f}
	log, _ := render(t, run, nil)
	if r := log.Runs[0].Results[0].Locations[0].PhysicalLocation.Region; r.StartColumn != 19 || r.EndColumn != 20 {
		t.Errorf("region = %+v, want columns 19-20 in code points (utf-16 20-21, bytes %v)", r, bytesSpan)
	}
}

// Two tools that report one rule on one line never share a fingerprint, and
// so never share an alert: the tool is the fingerprint's first input.
func TestFingerprintsPerTool(t *testing.T) {
	run := manyTools(2)
	log, _ := render(t, run, nil)
	a := log.Runs[0].Results[0].PartialFingerprints["primaryLocationLineHash"]
	b := log.Runs[1].Results[0].PartialFingerprints["primaryLocationLineHash"]
	if a == b || len(a) != 64 {
		t.Errorf("fingerprints %q and %q; want two distinct SHA-256 hex values", a, b)
	}
}

func TestLevel(t *testing.T) {
	for severity, want := range map[string]string{"error": "error", "warning": "warning", "info": "note", "hint": "note"} {
		if got := Level(severity); got != want {
			t.Errorf("Level(%s) = %s, want %s", severity, got, want)
		}
	}
}

// An invocation that ran to the end analyzed, whatever its gate said: a tool
// that exits non-zero on its findings, or fails the threshold, did its work.
func TestExecutionSuccessful(t *testing.T) {
	zero, one := 0, 1
	synthetic := report.Finding{Kind: "synthetic", Severity: "error", Message: "x exited 1 without parsable findings"}
	for _, tc := range []struct {
		name string
		inv  report.Invocation
		want bool
	}{
		{name: "passed", inv: report.Invocation{State: "ran", ExitCode: &zero, Success: true}, want: true},
		{name: "failed the threshold", inv: report.Invocation{State: "ran", ExitCode: &zero, FailureKind: "threshold"}, want: true},
		{
			name: "exited on its findings", want: true,
			inv: report.Invocation{State: "ran", ExitCode: &one, FailureKind: "exit", Findings: []report.Finding{finding("x", "R", "a.go", 1, "error", "m")}},
		},
		{name: "failed without a finding", inv: report.Invocation{State: "ran", ExitCode: &one, FailureKind: "exit", Findings: []report.Finding{synthetic}}},
		{name: "cached", inv: report.Invocation{State: "cached", Success: true}, want: true},
		{name: "cancelled", inv: report.Invocation{State: "cancelled"}},
	} {
		if got := invocation(tc.inv).ExecutionSuccessful; got != tc.want {
			t.Errorf("%s: executionSuccessful = %v, want %v", tc.name, got, tc.want)
		}
	}
}
