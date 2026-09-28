package markdown

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

func issue(path string, row int, severity string, reported bool, msg string) report.Finding {
	return report.Finding{
		Tool: "eslint", Source: "eslint", Code: "no-var", Severity: severity, Reported: reported, Kind: "issue", Message: msg,
		Location: report.Location{Path: path, Row: row, EndRow: row, Chars: &textpos.Span{Start: 3, End: 4}, Precision: "exact"},
	}
}

func sampleRun() *report.Run {
	one, two := 1, 2
	return &report.Run{
		Datamitsu:  report.Producer{Configuration: "my config"},
		Selection:  report.Selection{Mode: "all"},
		Complete:   false,
		Incomplete: []report.Reason{"operation-skipped"},
		Operations: []report.Operation{
			{
				Name: "lint", Ran: true,
				Skipped:   []report.Skip{{Tool: "trivy", Reason: "unsupported-platform", Detail: "no binary for windows/arm64"}},
				Cancelled: []report.Cancel{{Tool: "knip", Dir: "pkg/a", Started: true, Cause: "fail-fast"}},
				Tools: []report.ToolRun{
					{
						Name: "eslint", FailOn: "error", GateActive: true, Complete: true,
						Invocations: []report.Invocation{
							{State: "ran", ExitCode: &one, FailureKind: "exit", Findings: []report.Finding{
								issue("src/b.ts", 9, "error", true, "Unexpected var"),
								issue("src/a.ts", 3, "error", true, "Unexpected *var* <b>|x|</b>\n::error::x"),
								issue("src/a.ts", 4, "warning", false, "hidden"),
							}},
							{State: "cached", Success: true, Files: []report.FileResult{{Path: "src/c.ts"}, {Path: "src/d.ts"}}},
						},
					},
					{
						Name: "tsc", FailOn: "error", GateActive: true, Incomplete: []report.Reason{"no-extraction"},
						Invocations: []report.Invocation{{State: "ran", ExitCode: &two, FailureKind: "exit", Findings: []report.Finding{{
							Tool: "tsc", Source: "tsc", Severity: "error", Gates: true, Kind: "synthetic",
							Message: "tsc exited 2 without parsable findings", Location: report.Location{Precision: "unknown"},
						}}}},
					},
				},
			},
			{Name: "fix"},
		},
	}
}

func TestRender(t *testing.T) {
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, sampleRun(), nil); err != nil {
		t.Fatal(err)
	}
	want := "## datamitsu · my config\n\n" +
		"- **Selection:** the whole repository\n" +
		"- **Complete:** no — operation-skipped\n" +
		"\n### lint\n\n" +
		"| Tool | Status | Runs | Cached | Errors | Warnings | Info | Hints |\n" +
		"| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |\n" +
		"| `eslint` | ✗ failed | 1 | 2 | 2 | 1 | 0 | 0 |\n" +
		"| `tsc` | ✗ failed | 1 | 0 | 0 | 0 | 0 | 0 |\n" +
		"\n#### Findings\n" +
		"\n##### Errors\n" +
		"\n`src/a.ts`\n\n" +
		"- `src/a.ts:3:3` — `eslint(no-var)`: Unexpected \\*var\\* &lt;b&gt;\\|x\\|&lt;/b&gt; ::error::x\n" +
		"\n`src/b.ts`\n\n" +
		"- `src/b.ts:9:3` — `eslint(no-var)`: Unexpected var\n" +
		"\nNo file\n\n" +
		"- `tsc`: tsc exited 2 without parsable findings\n" +
		"\n_1 warning below the threshold not listed._\n" +
		"\n#### Skipped and stopped\n\n" +
		"- `trivy` skipped (unsupported-platform: no binary for windows/arm64)\n" +
		"- `knip` in `pkg/a` cancelled (fail-fast)\n" +
		"\n#### Incomplete\n\n" +
		"- `tsc`: no-extraction\n" +
		"\n### fix\n\n" +
		"_Did not run._\n"
	if out.String() != want {
		t.Errorf("document =\n%s\nwant\n%s", out.String(), want)
	}
}

// A page with a size limit gets what fits and a footer counting what did not;
// the document never exceeds the budget.
func TestWriteBudget(t *testing.T) {
	code := 1
	var fs []report.Finding
	for row := 1; row <= 200; row++ {
		fs = append(fs, issue(fmt.Sprintf("src/f%03d.ts", row), row, "error", true, strings.Repeat("x", 40)))
	}
	run := &report.Run{Selection: report.Selection{Mode: "all"}, Complete: true, Operations: []report.Operation{{
		Name: "lint", Ran: true,
		Tools: []report.ToolRun{{
			Name: "eslint", FailOn: "error", GateActive: true, Complete: true,
			Invocations: []report.Invocation{{State: "ran", ExitCode: &code, Findings: fs}},
		}},
	}}}

	var full bytes.Buffer
	if cut, err := Write(&full, run, 0); err != nil || cut != 0 {
		t.Fatalf("unlimited: cut %d, %v", cut, err)
	}
	const budget = 4096
	var out bytes.Buffer
	cut, err := Write(&out, run, budget)
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() > budget {
		t.Errorf("wrote %d bytes, over the budget of %d", out.Len(), budget)
	}
	listed := strings.Count(out.String(), "\n- `src/")
	if cut == 0 || listed+cut != 200 {
		t.Errorf("listed %d and cut %d of 200 findings", listed, cut)
	}
	if !strings.HasSuffix(out.String(), fmt.Sprintf("_%d findings cut: the page ran out of room._\n", cut)) {
		t.Errorf("no footer counting the cut findings, or one naming a report the run did not write:\n%s", out.String()[max(0, out.Len()-300):])
	}

	run.Exports = []report.Export{
		{Format: "json", Path: "out/run.json", Status: report.ExportWritten},
		{Format: "json", Path: "-", Status: report.ExportWritten},
		{Format: "markdown", Path: "out/run.md", Status: report.ExportWritten},
	}
	out.Reset()
	if _, err := Write(&out, run, budget); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "the page ran out of room. Every finding is in `out/run.json`._\n") {
		t.Errorf("the footer should name the run's own JSON file:\n%s", out.String()[max(0, out.Len()-300):])
	}
}

// A page without room for the note that says so gets nothing.
func TestWriteNoRoom(t *testing.T) {
	var out bytes.Buffer
	if _, err := Write(&out, sampleRun(), 10); !errors.Is(err, ErrNoRoom) || out.Len() != 0 {
		t.Errorf("Write with 10 bytes = %v, wrote %q; want ErrNoRoom and nothing", err, out.String())
	}
}

func TestCode(t *testing.T) {
	for in, want := range map[string]string{
		"a.go":      "`a.go`",
		"a`b":       "``a`b``",
		"`x":        "`` `x ``",
		"two\nline": "`two line`",
	} {
		if got := code(in); got != want {
			t.Errorf("code(%q) = %q, want %q", in, got, want)
		}
	}
}
