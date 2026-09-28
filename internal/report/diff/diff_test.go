package diff

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

func fp(c byte) string { return strings.Repeat(string(c), 64) }

func finding(tool string, c byte, row int) report.Finding {
	return report.Finding{
		Fingerprint: fp(c), Tool: tool, Source: tool, Code: "R" + string(c), Severity: "error", Kind: "issue",
		Message: "finding " + string(c), Location: report.Location{Path: "src/a.ts", Row: row},
	}
}

// run is a lint run whose tools report the given findings, complete over the
// whole repository.
func run(tools map[string][]report.Finding) *report.Run {
	r := &report.Run{Schema: report.SchemaVersion, Selection: report.Selection{Mode: "all"}, Complete: true}
	op := report.Operation{Name: "lint", Ran: true}
	for name, fs := range tools {
		op.Tools = append(op.Tools, report.ToolRun{
			Name: name, Complete: true, Incomplete: []report.Reason{},
			Invocations: []report.Invocation{{State: "ran", Findings: fs}},
		})
	}
	r.Operations = []report.Operation{op}
	return r
}

func TestDiffClasses(t *testing.T) {
	before := run(map[string][]report.Finding{
		"eslint": {finding("eslint", 'a', 3), finding("eslint", 'b', 5), finding("eslint", 'c', 9)},
		"gone":   {finding("gone", 'g', 1)},
	})
	after := run(map[string][]report.Finding{
		"eslint": {finding("eslint", 'a', 3), finding("eslint", 'b', 6), finding("eslint", 'd', 2)},
		"fresh":  {finding("fresh", 'f', 1)},
	})
	res := Diff(before, after)
	want := Counts{New: 1, Fixed: 1, Unchanged: 1, Moved: 1, Unobserved: 2}
	if res.Summary != want {
		t.Fatalf("summary = %+v, want %+v", res.Summary, want)
	}
	byName := map[string]Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	eslint := byName["eslint"]
	if eslint.New[0].Fingerprint != fp('d') || eslint.Fixed[0].Fingerprint != fp('c') ||
		eslint.Unchanged[0].Fingerprint != fp('a') || eslint.Moved[0].Fingerprint != fp('b') || eslint.Moved[0].BeforeRow != 5 {
		t.Errorf("eslint = %+v", eslint)
	}
	if byName["gone"].Status != StatusBeforeOnly || byName["fresh"].Status != StatusAfterOnly {
		t.Errorf("statuses = %s, %s", byName["gone"].Status, byName["fresh"].Status)
	}
}

// TestDiffUnknownWhereTheSecondRunDidNotLook: a finding that disappeared is
// fixed only where the second run's tool is complete over the whole
// repository; otherwise it is unknown, with the tool's reasons.
func TestDiffUnknownWhereTheSecondRunDidNotLook(t *testing.T) {
	before := run(map[string][]report.Finding{"eslint": {finding("eslint", 'a', 1)}})
	tests := []struct {
		name  string
		after func() *report.Run
	}{
		{"cancelled", func() *report.Run {
			r := run(map[string][]report.Finding{"eslint": nil})
			r.Operations[0].Tools[0].Complete = false
			r.Operations[0].Tools[0].Incomplete = []report.Reason{report.ReasonCancelled}
			return r
		}},
		{"narrowed", func() *report.Run {
			r := run(map[string][]report.Finding{"eslint": nil})
			r.Selection.Mode = "paths"
			return r
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Diff(before, tt.after())
			if res.Summary.Unknown != 1 || res.Summary.Fixed != 0 {
				t.Errorf("summary = %+v, want one unknown", res.Summary)
			}
		})
	}
}

// TestDiffSymmetric: for two complete runs, what one direction calls new the
// other calls fixed.
func TestDiffSymmetric(t *testing.T) {
	a := run(map[string][]report.Finding{"eslint": {finding("eslint", 'a', 1), finding("eslint", 'b', 2)}})
	b := run(map[string][]report.Finding{"eslint": {finding("eslint", 'b', 2), finding("eslint", 'c', 3), finding("eslint", 'd', 4)}})
	ab, ba := Diff(a, b), Diff(b, a)
	if ab.Summary.New != ba.Summary.Fixed || ab.Summary.Fixed != ba.Summary.New || ab.Summary.Unchanged != ba.Summary.Unchanged {
		t.Errorf("a→b = %+v, b→a = %+v", ab.Summary, ba.Summary)
	}
}

// TestDiffIgnoresSynthetic: a synthetic finding stands for a failure, not a
// finding of the code.
func TestDiffIgnoresSynthetic(t *testing.T) {
	synthetic := report.Finding{Fingerprint: fp('s'), Tool: "tsc", Kind: "synthetic", Severity: "error"}
	res := Diff(run(map[string][]report.Finding{"tsc": {synthetic}}), run(map[string][]report.Finding{"tsc": nil}))
	if res.Summary != (Counts{}) {
		t.Errorf("summary = %+v, want nothing", res.Summary)
	}
}

func TestWrite(t *testing.T) {
	before := run(map[string][]report.Finding{"eslint": {finding("eslint", 'a', 1)}})
	after := run(map[string][]report.Finding{"eslint": {finding("eslint", 'b', 2)}})
	res := Diff(before, after)

	var js bytes.Buffer
	if err := Write(&js, res, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(js.Bytes(), &back); err != nil || back.Schema != Schema || back.Summary != res.Summary {
		t.Errorf("json round trip = %+v, %v", back, err)
	}

	var md bytes.Buffer
	if err := Write(&md, res, FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 new · 1 fixed", "| `eslint` | 1 | 1 | 0 | 0 | 0 | 0 |", "### New", "`src/a.ts:2`", "### Fixed"} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("markdown lacks %q:\n%s", want, md.String())
		}
	}
	if err := Write(&md, res, "yaml"); err == nil {
		t.Error("an unknown format was written")
	}
}
