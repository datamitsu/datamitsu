package markdown

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"

	zerovalue_time "time"
)

func issue(path string, row int, severity string, reported bool, msg string) report.Finding {
	return report.Finding{
		Fingerprint:      "",
		FingerprintBasis: "",
		RuleURL:          "",
		Gates:            false,
		Baselined:        false,
		Provenance:       "",

		Tool: "eslint", Source: "eslint", Code: "no-var", Severity: severity, Reported: reported, Shown: reported, Kind: "issue", Message: msg,
		Location: report.Location{
			Col:    0,
			EndCol: 0,
			Unit:   "",
			Bytes:  nil,
			Utf16:  nil,
			Path:   path, Row: row, EndRow: row, Chars: &textpos.Span{Start: 3, End: 4}, Precision: "exact",
		},
	}
}

func sampleRun() *report.Run {
	one, two := 1, 2
	return &report.Run{
		Fingerprint:     "",
		StartedAt:       zerovalue_time.Time{},
		EndedAt:         zerovalue_time.Time{},
		FailFast:        false,
		RedactedReasons: nil,
		Exports:         nil,
		CI:              report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},

		Datamitsu: report.Producer{
			Version:       "",
			Configuration: "my config",
		},
		Selection: report.Selection{
			Dir:           "",
			Paths:         nil,
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          "all",
		},
		Complete:   false,
		Incomplete: []report.Reason{"operation-skipped"},
		Operations: []report.Operation{
			{
				Success:         false,
				Duration:        0,
				ChangesObserved: false,
				ChangesReason:   "",
				ChangesDetail:   "",
				ChangesScope:    nil,
				Changes:         nil,

				Name: "lint", Ran: true,
				Skipped: []report.Skip{{Tool: "trivy", Reason: "unsupported-platform", Detail: "no binary for windows/arm64"}},
				Cancelled: []report.Cancel{{
					TaskID: "",
					Tool:   "knip", Dir: "pkg/a", Started: true, Cause: "fail-fast",
				}},
				Tools: []report.ToolRun{
					{
						App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
						Parser:          nil,
						Category:        "",
						Incomplete:      nil,
						RedactedReasons: nil,

						Name: "eslint", FailOn: "error", GateActive: true, Complete: true,
						Invocations: []report.Invocation{
							{
								ID:                 "",
								TaskID:             "",
								Dir:                "",
								Scope:              "",
								Granularity:        "",
								Arity:              "",
								Coverage:           "",
								WholeUnit:          false,
								RedactedState:      0,
								Success:            false,
								Duration:           0,
								Extraction:         "",
								RedactedExtraction: 0,
								Provenance:         "",
								Files:              nil,
								Step:               0,
								Changes:            nil,
								OutputTail:         "",
								State:              "ran", ExitCode: &one, FailureKind: "exit", Findings: []report.Finding{
									issue("src/b.ts", 9, "error", true, "Unexpected var"),
									issue("src/a.ts", 3, "error", true, "Unexpected *var* <b>|x|</b>\n::error::x"),
									issue("src/a.ts", 4, "warning", false, "hidden"),
								},
							},
							{
								ID:                 "",
								TaskID:             "",
								Dir:                "",
								Scope:              "",
								Granularity:        "",
								Arity:              "",
								Coverage:           "",
								WholeUnit:          false,
								RedactedState:      0,
								ExitCode:           nil,
								FailureKind:        "",
								Duration:           0,
								Extraction:         "",
								RedactedExtraction: 0,
								Provenance:         "",
								Findings:           nil,
								Step:               0,
								Changes:            nil,
								OutputTail:         "",
								State:              "cached", Success: true, Files: []report.FileResult{{
									State:         "",
									RedactedState: 0,
									Success:       false,
									ExitCode:      nil,
									Patch:         "",
									Path:          "src/c.ts",
								}, {
									State:         "",
									RedactedState: 0,
									Success:       false,
									ExitCode:      nil,
									Patch:         "",
									Path:          "src/d.ts",
								}},
							},
						},
					},
					{
						App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
						Parser:          nil,
						Category:        "",
						Complete:        false,
						RedactedReasons: nil,

						Name: "tsc", FailOn: "error", GateActive: true, Incomplete: []report.Reason{"no-extraction"},
						Invocations: []report.Invocation{{
							ID:                 "",
							TaskID:             "",
							Dir:                "",
							Scope:              "",
							Granularity:        "",
							Arity:              "",
							Coverage:           "",
							WholeUnit:          false,
							RedactedState:      0,
							Success:            false,
							Duration:           0,
							Extraction:         "",
							RedactedExtraction: 0,
							Provenance:         "",
							Files:              nil,
							Step:               0,
							Changes:            nil,
							OutputTail:         "",
							State:              "ran", ExitCode: &two, FailureKind: "exit", Findings: []report.Finding{{
								Fingerprint:      "",
								FingerprintBasis: "",
								Code:             "",
								RuleURL:          "",
								Reported:         false,
								Shown:            false,
								Baselined:        false,
								Provenance:       "",

								Tool: "tsc", Source: "tsc", Severity: "error", Gates: true, Kind: "synthetic",
								Message: "tsc exited 2 without parsable findings", Location: report.Location{
									Path:      "",
									Row:       0,
									EndRow:    0,
									Col:       0,
									EndCol:    0,
									Unit:      "",
									Chars:     nil,
									Bytes:     nil,
									Utf16:     nil,
									Precision: "unknown",
								},
							}},
						}},
					},
				},
			},
			{
				Ran:             false,
				Success:         false,
				Duration:        0,
				Skipped:         nil,
				Cancelled:       nil,
				Tools:           nil,
				ChangesObserved: false,
				ChangesReason:   "",
				ChangesDetail:   "",
				ChangesScope:    nil,
				Changes:         nil,
				Name:            "fix",
			},
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
	run := &report.Run{
		Fingerprint:     "",
		Datamitsu:       report.Producer{Version: "", Configuration: ""},
		StartedAt:       zerovalue_time.Time{},
		EndedAt:         zerovalue_time.Time{},
		FailFast:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Exports:         nil,
		CI:              report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Selection: report.Selection{
			Dir:           "",
			Paths:         nil,
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          "all",
		}, Complete: true, Operations: []report.Operation{{
			Success:         false,
			Duration:        0,
			Skipped:         nil,
			Cancelled:       nil,
			ChangesObserved: false,
			ChangesReason:   "",
			ChangesDetail:   "",
			ChangesScope:    nil,
			Changes:         nil,

			Name: "lint", Ran: true,
			Tools: []report.ToolRun{{
				App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
				Parser:          nil,
				Category:        "",
				Incomplete:      nil,
				RedactedReasons: nil,

				Name: "eslint", FailOn: "error", GateActive: true, Complete: true,
				Invocations: []report.Invocation{{
					ID:                 "",
					TaskID:             "",
					Dir:                "",
					Scope:              "",
					Granularity:        "",
					Arity:              "",
					Coverage:           "",
					WholeUnit:          false,
					RedactedState:      0,
					Success:            false,
					FailureKind:        "",
					Duration:           0,
					Extraction:         "",
					RedactedExtraction: 0,
					Provenance:         "",
					Files:              nil,
					Step:               0,
					Changes:            nil,
					OutputTail:         "",
					State:              "ran", ExitCode: &code, Findings: fs,
				}},
			}},
		}},
	}

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
		{
			Detail:    "",
			Companion: "",
			Omitted:   nil,
			Format:    "json", Path: "out/run.json", Status: report.ExportWritten,
		},
		{
			Detail:    "",
			Companion: "",
			Omitted:   nil,
			Format:    "json", Path: "-", Status: report.ExportWritten,
		},
		{
			Detail:    "",
			Companion: "",
			Omitted:   nil,
			Format:    "markdown", Path: "out/run.md", Status: report.ExportWritten,
		},
	}
	out.Reset()
	if _, err := Write(&out, run, budget); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "the page ran out of room. Every finding is in `out/run.json`._\n") {
		t.Errorf("the footer should name the run's own JSON file:\n%s", out.String()[max(0, out.Len()-300):])
	}
}

// A document that fits its budget exactly is written whole.
func TestWriteExactFit(t *testing.T) {
	var full bytes.Buffer
	if _, err := Write(&full, sampleRun(), 0); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cut, err := Write(&out, sampleRun(), full.Len())
	if err != nil || cut != 0 || out.String() != full.String() {
		t.Errorf("Write at an exact fit cut %d (%v):\n%s", cut, err, out.String())
	}
}

// A tool with no binary for the host is listed without a run: it did not pass.
func TestPlatformSkippedToolIsNotPassed(t *testing.T) {
	run := sampleRun()
	run.Operations[0].Tools = append(run.Operations[0].Tools,
		report.ToolRun{
			App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
			Parser:          nil,
			FailOn:          "",
			GateActive:      false,
			Category:        "",
			Complete:        false,
			RedactedReasons: nil,
			Name:            "trivy", Incomplete: []report.Reason{"platform-skip"}, Invocations: []report.Invocation{},
		})
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, run, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "| `trivy` | ⊘ skipped | 0 | 0 |") {
		t.Errorf("the skipped tool's row:\n%s", out.String())
	}
}

// A pipe in a tool's name stays inside its table cell.
func TestTableCellEscapesPipes(t *testing.T) {
	run := sampleRun()
	run.Operations[0].Tools[0].Name = "lint|types"
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, run, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "| `lint\\|types` | ✗ failed |") {
		t.Errorf("the pipe is not escaped in its cell:\n%s", out.String())
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

func TestRenderCountsBaselined(t *testing.T) {
	f := issue("src/a.ts", 3, "error", false, "old")
	f.Baselined = true
	run := &report.Run{
		Fingerprint:     "",
		Datamitsu:       report.Producer{Version: "", Configuration: ""},
		StartedAt:       zerovalue_time.Time{},
		EndedAt:         zerovalue_time.Time{},
		FailFast:        false,
		Complete:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Exports:         nil,
		CI:              report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Selection: report.Selection{
			Dir:           "",
			Paths:         nil,
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          "all",
		}, Operations: []report.Operation{{
			Success:         false,
			Duration:        0,
			Skipped:         nil,
			Cancelled:       nil,
			ChangesObserved: false,
			ChangesReason:   "",
			ChangesDetail:   "",
			ChangesScope:    nil,
			Changes:         nil,
			Name:            "lint", Ran: true, Tools: []report.ToolRun{
				{
					App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
					Parser:          nil,
					FailOn:          "",
					GateActive:      false,
					Category:        "",
					Complete:        false,
					Incomplete:      nil,
					RedactedReasons: nil,
					Name:            "eslint", Invocations: []report.Invocation{{
						ID:                 "",
						TaskID:             "",
						Dir:                "",
						Scope:              "",
						Granularity:        "",
						Arity:              "",
						Coverage:           "",
						WholeUnit:          false,
						RedactedState:      0,
						ExitCode:           nil,
						FailureKind:        "",
						Duration:           0,
						Extraction:         "",
						RedactedExtraction: 0,
						Provenance:         "",
						Files:              nil,
						Step:               0,
						Changes:            nil,
						OutputTail:         "",
						State:              "ran", Success: true, Findings: []report.Finding{f},
					}},
				},
			},
		}},
	}
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, run, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "_1 finding held by the baseline not listed._") || strings.Contains(out.String(), "below the threshold") {
		t.Errorf("the baselined finding should be counted apart:\n%s", out.String())
	}
}

func TestRenderChangedFiles(t *testing.T) {
	fix := report.Operation{
		Success:       false,
		Duration:      0,
		Skipped:       nil,
		Cancelled:     nil,
		ChangesReason: "",
		ChangesDetail: "",
		ChangesScope:  nil,

		Name: "fix", Ran: true, ChangesObserved: true, Changes: []report.Change{{
			Patch: false,
			Step:  0,
			Path:  "b.ts", Kind: "created",
		}, {
			Patch: false,
			Step:  0,
			Path:  "config.json", Kind: "reverted",
		}},
		Tools: []report.ToolRun{{
			App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
			Parser:          nil,
			FailOn:          "",
			GateActive:      false,
			Category:        "",
			Complete:        false,
			Incomplete:      nil,
			RedactedReasons: nil,
			Name:            "prettier", Invocations: []report.Invocation{{
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				Coverage:           "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				FailureKind:        "",
				Duration:           0,
				Extraction:         "",
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				OutputTail:         "",

				State: "ran", Success: true,
				Changes: []report.Change{{
					Step: 0,
					Path: "a.ts", Kind: "modified", Patch: true,
				}},
			}},
		}},
	}
	unobserved := report.Operation{
		Success:         false,
		Duration:        0,
		Skipped:         nil,
		Cancelled:       nil,
		Tools:           nil,
		ChangesObserved: false,
		ChangesScope:    nil,
		Changes:         nil,
		Name:            "fix", Ran: true, ChangesReason: "snapshot-failed", ChangesDetail: "no repository",
	}
	lint := report.Operation{
		Success:         false,
		Duration:        0,
		Skipped:         nil,
		Cancelled:       nil,
		Tools:           nil,
		ChangesObserved: false,
		ChangesDetail:   "",
		ChangesScope:    nil,
		Changes:         nil,
		Name:            "lint", Ran: true, ChangesReason: report.ChangesNoFixTask,
	}
	var out bytes.Buffer
	run := &report.Run{
		Fingerprint:     "",
		Datamitsu:       report.Producer{Version: "", Configuration: ""},
		StartedAt:       zerovalue_time.Time{},
		EndedAt:         zerovalue_time.Time{},
		FailFast:        false,
		Complete:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Exports:         nil,
		CI:              report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Selection: report.Selection{
			Dir:           "",
			Paths:         nil,
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          "all",
		}, Operations: []report.Operation{fix, lint},
	}
	if err := (Renderer{}).Render(&out, run, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "#### Changed files\n\n- `a.ts` modified\n- `b.ts` created\n- `config.json` reverted\n") || strings.Count(out.String(), "Changed files") != 1 {
		t.Errorf("want tool and bundled changes in the fix's changed files, and none for lint:\n%s", out.String())
	}
	out.Reset()
	run.Operations = []report.Operation{unobserved}
	if err := (Renderer{}).Render(&out, run, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "_Not observed: snapshot-failed: no repository._") {
		t.Errorf("want the reason the changes were not observed:\n%s", out.String())
	}
}
