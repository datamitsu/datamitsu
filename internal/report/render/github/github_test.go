// cspell:ignore Aerror

package github

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

func TestEscape(t *testing.T) {
	tests := []struct {
		in, data, property string
	}{
		{in: "plain", data: "plain", property: "plain"},
		{in: "100%", data: "100%25", property: "100%25"},
		{in: "a\nb", data: "a%0Ab", property: "a%0Ab"},
		{in: "a\r\nb", data: "a%0D%0Ab", property: "a%0D%0Ab"},
		{in: "go vet: x, y", data: "go vet: x, y", property: "go vet%3A x%2C y"},
		{in: "%0A", data: "%250A", property: "%250A"},
		{in: "::error::x", data: "::error::x", property: "%3A%3Aerror%3A%3Ax"},
	}
	for _, tt := range tests {
		if got := EscapeData(tt.in); got != tt.data {
			t.Errorf("EscapeData(%q) = %q, want %q", tt.in, got, tt.data)
		}
		if got := EscapeProperty(tt.in); got != tt.property {
			t.Errorf("EscapeProperty(%q) = %q, want %q", tt.in, got, tt.property)
		}
	}
}

func span(start, end int) *textpos.Span { return &textpos.Span{Start: start, End: end} }

func TestCommand(t *testing.T) {
	tests := []struct {
		name string
		loc  report.Location
		code string
		msg  string
		want string
	}{
		{
			name: "one line with exact columns",
			loc:  report.Location{Path: "pkg/a.go", Row: 3, EndRow: 3, Chars: span(5, 9), Precision: "exact"},
			code: "errcheck", msg: "unchecked",
			want: "::error file=pkg/a.go,line=3,col=5,endColumn=9,title=golangci-lint(errcheck)::unchecked",
		},
		{
			name: "an ASCII line counts alike in every unit",
			loc:  report.Location{Path: "a.go", Row: 3, EndRow: 3, Chars: span(5, 9), Precision: "ascii"},
			want: "::error file=a.go,line=3,col=5,endColumn=9,title=golangci-lint::m",
		},
		{
			name: "columns of unknown precision are left out",
			loc:  report.Location{Path: "a.go", Row: 3, EndRow: 3, Col: 5, EndCol: 9, Precision: "unknown"},
			want: "::error file=a.go,line=3,title=golangci-lint::m",
		},
		{
			name: "a span over lines keeps its lines and drops its columns",
			loc:  report.Location{Path: "a.go", Row: 3, EndRow: 5, Chars: span(5, 2), Precision: "exact"},
			want: "::error file=a.go,line=3,endLine=5,title=golangci-lint::m",
		},
		{
			name: "a point has no end column",
			loc:  report.Location{Path: "a.go", Row: 3, EndRow: 3, Chars: span(5, 5), Precision: "exact"},
			want: "::error file=a.go,line=3,col=5,title=golangci-lint::m",
		},
		{
			name: "a file outside the repository is not named",
			loc:  report.Location{Path: "/elsewhere/a.go", Row: 3, EndRow: 3, Chars: span(5, 9), Precision: "exact"},
			want: "::error title=golangci-lint::m",
		},
		{
			name: "a Windows path outside the repository is not named",
			loc:  report.Location{Path: `C:\elsewhere\a.go`, Row: 3},
			want: "::error title=golangci-lint::m",
		},
		{
			name: "no file",
			loc:  report.Location{Precision: "unknown"},
			msg:  "golangci-lint exited 3 without parsable findings",
			want: "::error title=golangci-lint::golangci-lint exited 3 without parsable findings",
		},
		{
			name: "a file name and a title with separators",
			loc:  report.Location{Path: "dir,x/a:b.go", Row: 1, EndRow: 1},
			code: "a,b:c",
			want: "::error file=dir%2Cx/a%3Ab.go,line=1,title=golangci-lint(a%2Cb%3Ac)::m",
		},
		{
			name: "a message with a newline and a command stays one command",
			loc:  report.Location{Path: "a.go", Row: 1, EndRow: 1},
			msg:  "first\n::error::x",
			want: "::error file=a.go,line=1,title=golangci-lint::first%0A::error::x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.msg
			if msg == "" {
				msg = "m"
			}
			a := fromFinding(report.Finding{Tool: "golangci-lint", Source: "golangci-lint", Code: tt.code, Severity: "error", Message: msg, Location: tt.loc})
			if got := a.Command(); got != tt.want {
				t.Errorf("Command() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// A message is cut where GitHub cuts it, without splitting a character, so the
// bytes written never exceed the characters it keeps.
func TestCommandCutsTheMessage(t *testing.T) {
	msg := strings.Repeat("a", MessageLimit-1) + "é" + "tail"
	a := Annotation{Level: LevelError, Source: "t", Message: msg}
	got := strings.TrimPrefix(a.Command(), "::error title=t::")
	if got != strings.Repeat("a", MessageLimit-1) {
		t.Errorf("message cut to %d bytes ending %q, want the %d ASCII bytes before the split character", len(got), got[len(got)-3:], MessageLimit-1)
	}
	short := Annotation{Level: LevelError, Source: "t", Message: "é"}
	if !strings.HasSuffix(short.Command(), "::é") {
		t.Errorf("a short message was changed: %q", short.Command())
	}
}

// The file is the report's path, relative to the repository root, whatever
// directory the workflow checked the repository out into: with
// actions/checkout's `path: sub` the workspace is the directory above the
// root, and a path relative to it would name no file of the repository.
func TestFileIsRelativeToTheRepositoryRoot(t *testing.T) {
	root, workspace := "/w/sub", "/w"
	rel := report.RelPath(root, "/w/sub/pkg/a.go")
	a := fromFinding(report.Finding{Source: "t", Severity: "error", Location: report.Location{Path: rel, Row: 1, EndRow: 1}})
	if a.File != "pkg/a.go" {
		t.Errorf("file = %q, want pkg/a.go (not relative to the workspace %s)", a.File, workspace)
	}
}

func finding(file string, row int, severity string) report.Finding {
	return report.Finding{
		Fingerprint: fmt.Sprintf("%s:%d:%s", file, row, severity), Tool: "t", Source: "t", Code: "c",
		Severity: severity, Reported: true, Kind: "issue", Message: "m",
		Location: report.Location{Path: file, Row: row, EndRow: row, Precision: "unknown"},
	}
}

func failedRun(findings ...report.Finding) *report.Run {
	code := 1
	return &report.Run{Operations: []report.Operation{{
		Name: "lint", Ran: true,
		Tools: []report.ToolRun{{
			Name: "t", FailOn: "error", GateActive: true,
			Invocations: []report.Invocation{{State: "ran", ExitCode: &code, Findings: findings}},
		}},
	}}}
}

// 25 errors in five files, two of them touched: the touched files come first,
// one finding each in turn, then one finding of every other file; the notice
// takes the first notice slot, and its text names no report when none was
// asked for.
func TestSelectBudget(t *testing.T) {
	var fs []report.Finding
	counts := map[string]int{"a.go": 4, "b.go": 3, "c.go": 6, "d.go": 7, "e.go": 5}
	for file, n := range counts {
		for row := 1; row <= n; row++ {
			fs = append(fs, finding(file, row, "error"))
		}
	}
	fs = append(fs, finding("n.go", 1, "info"))
	sel := Select(Candidates(failedRun(fs...)), map[string]bool{"a.go": true, "b.go": true})
	if sel.Candidates != 26 || sel.Omitted != 15 {
		t.Fatalf("candidates %d, omitted %d; want 26 and 15", sel.Candidates, sel.Omitted)
	}
	got := make([]string, 0, len(sel.Annotations))
	for _, a := range sel.Annotations {
		got = append(got, fmt.Sprintf("%s:%s:%d", a.Level, a.File, a.Line))
	}
	want := []string{
		"error:a.go:1", "error:b.go:1", "error:a.go:2", "error:b.go:2", "error:a.go:3", "error:b.go:3", "error:a.go:4",
		"error:c.go:1", "error:d.go:1", "error:e.go:1",
		"notice:n.go:1",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("selection =\n%v\nwant\n%v", got, want)
	}

	var out bytes.Buffer
	if err := Print(&out, sel, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 12 {
		t.Fatalf("printed %d lines, want 12:\n%s", len(lines), out.String())
	}
	if lines[10] != "::notice::datamitsu: 15 more findings not annotated" {
		t.Errorf("the notice slot holds %q", lines[10])
	}
	if !strings.HasPrefix(lines[11], "::notice file=n.go,") {
		t.Errorf("the notice finding should follow the omitted notice, got %q", lines[11])
	}

	out.Reset()
	_ = Print(&out, sel, []string{"the step summary", "out/run.json"})
	if !strings.Contains(out.String(), "::notice::datamitsu: 15 more findings in the step summary and in out/run.json\n") {
		t.Errorf("the notice should name where the rest is:\n%s", out.String())
	}
}

// Eleven notices and nothing else: the notice slot costs one of them, and ten
// notices fit without it.
func TestSelectNoticeSlot(t *testing.T) {
	var eleven []report.Finding
	for row := 1; row <= 11; row++ {
		eleven = append(eleven, finding("a.go", row, "hint"))
	}
	sel := Select(Candidates(failedRun(eleven...)), nil)
	if len(sel.Annotations) != 9 || sel.Omitted != 2 {
		t.Errorf("kept %d and omitted %d of eleven notices; want 9 and 2", len(sel.Annotations), sel.Omitted)
	}
	sel = Select(Candidates(failedRun(eleven[:10]...)), nil)
	var out bytes.Buffer
	_ = Print(&out, sel, nil)
	if len(sel.Annotations) != 10 || sel.Omitted != 0 || strings.Contains(out.String(), "more finding") {
		t.Errorf("ten notices should all fit without a notice slot:\n%s", out.String())
	}
}

// The candidates are what the terminal shows: a failed invocation's findings at
// or above its threshold, and a synthetic finding, once each.
func TestCandidates(t *testing.T) {
	warn := finding("a.go", 2, "warning")
	warn.Reported = false
	dup := finding("a.go", 1, "error")
	synthetic := report.Finding{
		Tool: "t", Source: "t", Severity: "error", Kind: "synthetic", Gates: true,
		Fingerprint: "syn", Message: "t exited 2 without parsable findings", Location: report.Location{Precision: "unknown"},
	}
	run := failedRun(finding("a.go", 1, "error"), warn)
	code := 2
	run.Operations[0].Tools[0].Invocations = append(run.Operations[0].Tools[0].Invocations,
		report.Invocation{State: "ran", ExitCode: &code, Findings: []report.Finding{dup, synthetic}})
	candidates := Candidates(run)
	got := make([]string, 0, len(candidates))
	for _, a := range candidates {
		got = append(got, a.Command())
	}
	want := []string{
		"::error file=a.go,line=1,title=t(c)::m",
		"::error title=t::t exited 2 without parsable findings",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("candidates =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
