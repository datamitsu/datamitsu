package common

import (
	"reflect"
	"testing"
	zerovalue_time "time"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

func TestListedOperation(t *testing.T) {
	tests := []struct {
		ops  []string
		want string
	}{
		{ops: []string{"fix", "lint"}, want: "lint"},
		{ops: []string{"lint"}, want: "lint"},
		{ops: []string{"fix"}, want: "fix"},
		{ops: nil, want: ""},
	}
	for _, tt := range tests {
		if got := ListedOperationName(tt.ops); got != tt.want {
			t.Errorf("ListedOperationName(%v) = %q, want %q", tt.ops, got, tt.want)
		}
		run := &report.Run{Schema: "", Fingerprint: "", Datamitsu: report.Producer{Version: "", Configuration: ""}, StartedAt: zerovalue_time.Time{}, EndedAt: zerovalue_time.Time{}, Selection: report.Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil}, FailFast: false, Complete: false, Incomplete: nil, Operations: nil, Exports: nil, CI: report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""}}
		for _, name := range tt.ops {
			run.Operations = append(run.Operations, report.Operation{Ran: false, Success: false, Duration: 0, Skipped: nil, Cancelled: nil, Tools: nil, ChangesObserved: false, ChangesReason: "", ChangesDetail: "", ChangesScope: nil, Changes: nil, Name: name})
		}
		got := ListedOperation(run)
		if (got == nil) != (tt.want == "") || (got != nil && got.Name != tt.want) {
			t.Errorf("ListedOperation(%v) = %+v, want %q", tt.ops, got, tt.want)
		}
	}
}

func TestURIs(t *testing.T) {
	for _, tt := range []struct{ path, want string }{
		{"src/a.ts", "src/a.ts"},
		{"my dir/a#b?.ts", "my%20dir/a%23b%3F.ts"},
		{"c:ab/x", "c%3Aab/x"},
		{"a/b:c", "a/b:c"},
		{"100%.md", "100%25.md"},
	} {
		if got := RelativeURI(tt.path); got != tt.want {
			t.Errorf("RelativeURI(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
	for _, tt := range []struct{ path, want string }{
		{"/home/me/a b.go", "file:///home/me/a%20b.go"},
		{`C:\work\a.go`, "file:///C:/work/a.go"},
		{"D:/work/a.go", "file:///D:/work/a.go"},
		{`/tmp/a\b.go`, "file:///tmp/a%5Cb.go"},
		{`\\host\share\a.go`, "file://host/share/a.go"},
		{`\work\a.go`, "file:///work/a.go"},
	} {
		if got := FileURI(tt.path); got != tt.want {
			t.Errorf("FileURI(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
	for path, want := range map[string]bool{"/a": true, `\\server\x`: true, `C:\x`: true, "C:/x": true, "a/b": false, "": false, "c:x": false} {
		if Outside(path) != want {
			t.Errorf("Outside(%q) = %v, want %v", path, !want, want)
		}
	}
}

func TestRegionOf(t *testing.T) {
	sp := func(s, e int) *textpos.Span { return &textpos.Span{Start: s, End: e} }
	tests := []struct {
		name string
		loc  report.Location
		want Region
	}{
		{name: "span", loc: report.Location{Path: "", Col: 0, EndCol: 0, Unit: "", Bytes: nil, Utf16: nil, Row: 3, EndRow: 3, Chars: sp(5, 9), Precision: "exact"}, want: Region{Line: 3, Col: 5, EndLine: 3, EndCol: 9}},
		{name: "point", loc: report.Location{Path: "", Col: 0, EndCol: 0, Unit: "", Bytes: nil, Utf16: nil, Row: 3, EndRow: 3, Chars: sp(5, 5), Precision: "ascii"}, want: Region{EndCol: 0, Line: 3, Col: 5, EndLine: 3}},
		{name: "lines", loc: report.Location{Path: "", Col: 0, EndCol: 0, Unit: "", Bytes: nil, Utf16: nil, Row: 3, EndRow: 6, Chars: sp(5, 1), Precision: "exact"}, want: Region{Line: 3, Col: 5, EndLine: 6, EndCol: 1}},
		{name: "unknown", loc: report.Location{Path: "", Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Row: 3, EndRow: 4, Col: 5, EndCol: 9, Precision: "unknown"}, want: Region{Col: 0, EndCol: 0, Line: 3, EndLine: 4}},
		{name: "no row", loc: report.Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: "unknown"}, want: Region{Line: 0, Col: 0, EndLine: 0, EndCol: 0}},
	}
	for _, tt := range tests {
		if got := RegionOf(tt.loc, Chars); got != tt.want {
			t.Errorf("%s: RegionOf = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestFindingsOrder(t *testing.T) {
	f := func(tool, path string, row int) report.Finding {
		return report.Finding{Fingerprint: "", FingerprintBasis: "", Code: "", RuleURL: "", Severity: "", Reported: false, Gates: false, Shown: false, Baselined: false, Kind: "", Message: "", Provenance: "", Tool: tool, Source: tool, Location: report.Location{EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: "", Path: path, Row: row}}
	}
	op := &report.Operation{
		Name:            "",
		Ran:             false,
		Success:         false,
		Duration:        0,
		Skipped:         nil,
		Cancelled:       nil,
		ChangesObserved: false,
		ChangesReason:   "",
		ChangesDetail:   "",
		ChangesScope:    nil,
		Changes:         nil, Tools: []report.ToolRun{
			{App: report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Incomplete: nil, Name: "b", Invocations: []report.Invocation{{ID: "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", Findings: []report.Finding{f("b", "", 0), f("b", "z.go", 1), f("b", "a.go", 2)}}}},
			{App: report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Incomplete: nil, Name: "a", Invocations: []report.Invocation{{ID: "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", Findings: []report.Finding{f("a", "a.go", 2), f("a", "a.go", 1)}}}},
		},
	}
	found := Findings(op)
	got := make([]string, 0, len(found))
	for _, l := range found {
		got = append(got, l.Tool.Name+":"+l.Finding.Location.Path)
	}
	want := []string{"a:a.go", "a:a.go", "b:a.go", "b:z.go", "b:"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestRuleAndSource(t *testing.T) {
	if got := Rule(report.Finding{Fingerprint: "", FingerprintBasis: "", RuleURL: "", Severity: "", Reported: false, Gates: false, Shown: false, Baselined: false, Kind: "", Message: "", Location: report.Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: ""}, Provenance: "", Tool: "t", Source: "s", Code: "c"}); got != "s/c" {
		t.Errorf("Rule = %q", got)
	}
	if got := Rule(report.Finding{Fingerprint: "", FingerprintBasis: "", Source: "", Code: "", RuleURL: "", Severity: "", Reported: false, Gates: false, Shown: false, Baselined: false, Kind: "", Message: "", Location: report.Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: ""}, Provenance: "", Tool: "t"}); got != "t" {
		t.Errorf("Rule without source or code = %q", got)
	}
}

func TestCompanion(t *testing.T) {
	lint := report.Operation{
		Success:         false,
		Duration:        0,
		Skipped:         nil,
		Cancelled:       nil,
		ChangesObserved: false,
		ChangesReason:   "",
		ChangesDetail:   "",
		ChangesScope:    nil,
		Changes:         nil, Name: "lint", Ran: true, Tools: []report.ToolRun{
			{App: report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Incomplete: nil, Invocations: nil, Name: "eslint", Complete: true},
			{App: report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Invocations: nil, Name: "tsc", Incomplete: []report.Reason{"cancelled"}},
		},
	}
	fixNotRun := report.Operation{Ran: false, Success: false, Duration: 0, Skipped: nil, Cancelled: nil, Tools: nil, ChangesObserved: false, ChangesReason: "", ChangesDetail: "", ChangesScope: nil, Changes: nil, Name: "fix"}
	run := &report.Run{
		Schema:      "",
		Fingerprint: "",
		Datamitsu:   report.Producer{Version: "", Configuration: ""},
		StartedAt:   zerovalue_time.Time{},
		EndedAt:     zerovalue_time.Time{},
		FailFast:    false,
		Complete:    false,
		CI:          report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Selection:   report.Selection{Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil, Mode: "all"},
		Incomplete:  []report.Reason{"operation-skipped"},
		Operations:  []report.Operation{fixNotRun, lint},
		Exports:     []report.Export{{Detail: "", Companion: "", Omitted: nil, Format: "codequality", Path: "cq.json", Status: "written"}},
	}
	c := NewCompanion(run, "codequality", []*report.Operation{&run.Operations[1]}, nil)
	want := Companion{
		Complete: false,
		Omitted:  nil,
		Schema:   CompanionSchema, Format: "codequality", Incomplete: []report.Reason{},
		Tools: []CompanionTool{
			{Operation: "lint", Name: "eslint", Complete: true, Incomplete: []report.Reason{}},
			{Complete: false, Operation: "lint", Name: "tsc", Incomplete: []report.Reason{"cancelled"}},
		},
		Exports: run.Exports,
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("companion = %+v\nwant %+v", c, want)
	}
	if got := c.IncompleteTools(); len(got) != 1 || got[0].Name != "tsc" {
		t.Errorf("IncompleteTools = %+v", got)
	}

	// Complete when every tool it lists is: an operation it does not hold did
	// not run, which says nothing about it.
	run.Operations[1].Tools = run.Operations[1].Tools[:1]
	if c := NewCompanion(run, "codequality", []*report.Operation{&run.Operations[1]}, nil); !c.Complete {
		t.Errorf("companion = %+v, want complete", c)
	}
	if c := NewCompanion(run, "junit", []*report.Operation{&run.Operations[0], &run.Operations[1]}, nil); c.Complete ||
		!reflect.DeepEqual(c.Incomplete, []report.Reason{"operation-skipped"}) {
		t.Errorf("companion of both operations = %+v, want operation-skipped", c)
	}
	run.Selection.Mode = ""
	if c := NewCompanion(run, "codequality", []*report.Operation{&run.Operations[1]}, nil); c.Complete {
		t.Errorf("a document without its selection read as complete: %+v", c)
	}
}
