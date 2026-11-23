package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/report"
	reportJSON "github.com/datamitsu/datamitsu/internal/report/render/json"
)

// This file freezes --report sarif: which tools a SARIF file holds, under
// which category, and how a run of more tools than one file holds is split or
// refused.

type sarifDoc struct {
	Version string `json:"version"`
	Runs    []struct {
		AutomationDetails struct {
			ID string `json:"id"`
		} `json:"automationDetails"`
		ColumnKind string `json:"columnKind"`
		Tool       struct {
			Driver struct {
				Name           string `json:"name"`
				InformationURI string `json:"informationUri"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID              string            `json:"ruleId"`
			PartialFingerprints map[string]string `json:"partialFingerprints"`
		} `json:"results"`
	} `json:"runs"`
}

func (e *execProject) sarif(rel string) (string, sarifDoc) {
	e.t.Helper()
	raw := e.read(rel)
	var doc sarifDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		e.t.Fatalf("%s is not JSON: %v\n%s", rel, err, raw)
	}
	return e.normalize(raw), doc
}

func (d sarifDoc) tools() []string {
	out := make([]string, 0, len(d.Runs))
	for _, r := range d.Runs {
		out = append(out, r.Tool.Driver.Name)
	}
	return out
}

// sarifProject is reportProject with the parsed tool's app naming its
// official URL.
func sarifProject(t *testing.T, extra ...string) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	spec.Extra = `c.apps.hadolint.officialUrl = "https://example.com/hadolint";` + "\n"
	tools := append([]string{
		clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}),
		parsedTool(hadolintFinding, 0),
	}, extra...)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tools...))
	return e
}

// TestReportSARIF: a SARIF file holds one run per complete tool of the lint
// operation, under the category datamitsu/, with each finding's fingerprint
// as GitHub's primaryLocationLineHash; a tool whose completeness is not
// established is left out, said once on stderr and recorded in the exports.
func TestReportSARIF(t *testing.T) {
	t.Run("lint", func(t *testing.T) {
		e := sarifProject(t)
		res := e.run("", nil, "lint", "--report", "sarif=out/results.sarif", "--report", "json=out/run.json")
		e.wantExit(res, 0)
		raw, doc := e.sarif("out/results.sarif")
		if got := doc.tools(); !slices.Equal(got, []string{"datamitsu", "hadolint"}) || len(doc.Runs[0].Results) != 0 {
			t.Fatalf("runs = %v, want clean datamitsu and hadolint; alpha has no parser", got)
		}
		run := doc.Runs[1]
		if run.AutomationDetails.ID != "datamitsu/" || run.ColumnKind != "unicodeCodePoints" ||
			run.Tool.Driver.InformationURI != "https://example.com/hadolint" {
			t.Errorf("run = %+v", run)
		}
		_, report := e.report("out/run.json")
		fingerprint := findingsOf(t, report)[0]["fingerprint"]
		if len(run.Results) != 1 || run.Results[0].RuleID != "DL3006" || run.Results[0].PartialFingerprints["primaryLocationLineHash"] != fingerprint {
			t.Errorf("results = %+v, want DL3006 with the report's fingerprint %v", run.Results, fingerprint)
		}
		const note = "WARN report: lint tool alpha is incomplete (no-extraction): left out of sarif"
		if strings.Count(res.Stderr, note) != 1 {
			t.Errorf("stderr should say once that alpha was left out:\n%s", res.Stderr)
		}
		if !strings.Contains(e.read("out/run.json"), `"omitted": [`) {
			t.Errorf("the exports do not record the tool left out:\n%s", e.read("out/run.json"))
		}
		e.goldenReport("sarif_lint", raw)
		e.golden("report_sarif_lint", res)
	})

	// check writes the lint operation alone: two runs of one tool in one
	// category are rejected.
	t.Run("check", func(t *testing.T) {
		e := sarifProject(t, clitest.ShellTool("gamma", passScript, clitest.ToolOpSpec{Operation: "fix"}),
			clitest.ShellTool("delta", settle+clitest.RecordRun+"; echo '[]'",
				clitest.ToolOpSpec{Operation: "fix", Parser: "hadolint"}))
		res := e.run("", nil, "check", "--report", "sarif=r.sarif")
		e.wantExit(res, 0)
		if _, doc := e.sarif("r.sarif"); strings.Join(doc.tools(), ",") != "datamitsu,hadolint" {
			t.Errorf("runs = %v, want only the lint operation's tools", doc.tools())
		}
	})

	t.Run("category", func(t *testing.T) {
		e := sarifProject(t)
		res := e.run("", nil, "lint", "--report", "sarif=r.sarif?category=lint-linux")
		e.wantExit(res, 0)
		if _, doc := e.sarif("r.sarif"); doc.Runs[0].AutomationDetails.ID != "lint-linux/" {
			t.Errorf("automationDetails.id = %q, want lint-linux/", doc.Runs[0].AutomationDetails.ID)
		}
		bad := e.run("", nil, "lint", "--report", "sarif=r.sarif?category=")
		e.wantExit(bad, 2)
	})

	// A parsed tool that failed while its parser, which recognized what it
	// printed, found nothing in it is not complete: a run of it without
	// results would close every alert it has.
	t.Run("failed_without_findings", func(t *testing.T) {
		e := sarifProject(t, clitest.ShellTool("crasher", settle+clitest.RecordRun+"; echo '[]'; exit 2",
			clitest.ToolOpSpec{Parser: "hadolint"}))
		res := e.run("", nil, "lint", "--report", "sarif=r.sarif", "--report", "json=run.json")
		e.wantExit(res, 1)
		if _, doc := e.sarif("r.sarif"); strings.Join(doc.tools(), ",") != "datamitsu,hadolint" {
			t.Errorf("runs = %v, want clean datamitsu and hadolint", doc.tools())
		}
		if !strings.Contains(res.Stderr, "lint tool crasher is incomplete (failed-without-findings): left out of sarif") {
			t.Errorf("stderr:\n%s", res.Stderr)
		}
		if doc := e.read("run.json"); !strings.Contains(doc, `"failed-without-findings"`) {
			t.Errorf("the own JSON does not say why crasher is incomplete:\n%s", doc)
		}

		// A document an earlier build wrote called crasher complete; read
		// back, it is not.
		var run report.Run
		if err := json.Unmarshal([]byte(e.read("run.json")), &run); err != nil {
			t.Fatal(err)
		}
		for i := range run.Operations[0].Tools {
			if tr := &run.Operations[0].Tools[i]; tr.Name == "crasher" {
				tr.Complete, tr.Incomplete = true, []report.Reason{}
			}
		}
		var old bytes.Buffer
		if err := (reportJSON.Renderer{}).Render(&old, &run, nil); err != nil {
			t.Fatal(err)
		}
		e.p.WriteFile("old.json", old.String())
		rendered := e.run("", nil, "report", "render", "--input", "old.json", "--format", "sarif")
		e.wantExit(rendered, 0)
		if !strings.Contains(rendered.Stdout, `"name": "hadolint"`) || strings.Contains(rendered.Stdout, `"name": "crasher"`) {
			t.Errorf("replay should keep hadolint and omit crasher:\n%s", rendered.Stdout)
		}
	})

	// --tools narrows the run, not the selected tools' own runs: a selected
	// tool that is complete is written, and the others' alerts are left alone.
	t.Run("tools", func(t *testing.T) {
		e := sarifProject(t)
		res := e.run("", nil, "lint", "--tools", "hadolint", "--report", "sarif=r.sarif")
		e.wantExit(res, 0)
		if _, doc := e.sarif("r.sarif"); strings.Join(doc.tools(), ",") != "datamitsu,hadolint" {
			t.Errorf("runs = %v, want built-in checks and the selected hadolint", doc.tools())
		}
	})

	// A narrowed run is not refused: every tool in it is incomplete, and
	// code scanning refuses a file without a run, so no file is written — and
	// an earlier run's at the path goes, since an upload of it would stand for
	// this run. The export says so, and every alert stays as it is.
	t.Run("narrowed", func(t *testing.T) {
		e := sarifProject(t)
		e.p.WriteFile("r.sarif", "an earlier run's file")
		e.p.WriteFile("split/datamitsu-1.sarif", "an earlier run's file")
		res := e.run("", nil, "lint", "Dockerfile", "--report", "sarif=r.sarif", "--report", "json=run.json", "--allow-partial")
		e.wantExit(res, 0)
		if _, err := os.Stat(filepath.Join(e.p.Dir, "r.sarif")); !os.IsNotExist(err) {
			t.Errorf("r.sarif is still there: %v", err)
		}
		if !strings.Contains(res.Stderr, "lint tool hadolint is incomplete (narrowed-selection): left out of sarif") ||
			!strings.Contains(res.Stderr, "WARN report: sarif is not written: it would hold no tool run, which code scanning refuses") {
			t.Errorf("stderr:\n%s", res.Stderr)
		}
		if doc := e.read("run.json"); !strings.Contains(doc, `"format": "sarif",
      "path": "r.sarif",
      "status": "omitted",`) {
			t.Errorf("the export of the SARIF report is not omitted:\n%s", doc)
		}

		split := e.run("", nil, jsonl("lint", "Dockerfile", "--report", "sarif=split/")...)
		e.wantExit(split, 0)
		if entries, _ := os.ReadDir(filepath.Join(e.p.Dir, "split")); len(entries) != 0 {
			t.Errorf("split/ holds %v, want no file", entries)
		}
		wantReportEvent(t, clitest.MustParseJSONL(t, split.Stderr), "sarif", "split/", "omitted", "no tool run")
	})
}

// manyParsedTools declares n parsed tools that report nothing, named t01…tNN
// in reverse order, so the split cannot follow the declaration.
func manyParsedTools(n int) []string {
	var tools []string
	for i := n; i >= 1; i-- {
		tools = append(tools, clitest.ShellTool(fmt.Sprintf("t%02d", i), settle+clitest.RecordRun+"; echo '[]'",
			clitest.ToolOpSpec{Parser: "hadolint"}))
	}
	return tools
}

// TestReportSARIFSplit: GitHub reads at most twenty runs from one file. A run
// that plans more tools is refused as one file before anything runs, and a
// directory target splits it into files of twenty, tools sorted by name.
func TestReportSARIFSplit(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, manyParsedTools(21)...))

	refused := e.run("", nil, "lint", "--report", "sarif=r.sarif", "--report", "json=run.json")
	e.wantExit(refused, 2)
	e.wantMarker("t01", "")
	for _, name := range []string{"r.sarif", "run.json"} {
		if _, err := os.Stat(filepath.Join(e.p.Dir, name)); err == nil {
			t.Errorf("%s was written by a refused run", name)
		}
	}
	e.golden("report_sarif_crowded", refused)

	events := e.run("", nil, jsonl("lint", "--report", "sarif=-")...)
	e.wantExit(events, 2)
	wantReportEvent(t, clitest.MustParseJSONL(t, events.Stderr), "sarif", "-", "refused", "21 tools in stdout")

	// A file an earlier run left in the directory that this one does not
	// write is removed: an upload of the directory would read it.
	e.p.WriteFile("out/datamitsu-3.sarif", "stale")
	res := e.run("", nil, "lint", "--report", "sarif=out/")
	e.wantExit(res, 0)
	entries, err := os.ReadDir(filepath.Join(e.p.Dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	if strings.Join(files, ",") != "datamitsu-1.sarif,datamitsu-2.sarif" {
		t.Fatalf("out/ = %v, want two files", files)
	}
	_, first := e.sarif("out/datamitsu-1.sarif")
	_, second := e.sarif("out/datamitsu-2.sarif")
	if got := first.tools(); len(got) != 20 || got[0] != "datamitsu" || got[1] != "t01" || got[19] != "t19" {
		t.Errorf("datamitsu-1.sarif = %v, want datamitsu and t01…t19", got)
	}
	if got := second.tools(); !slices.Equal(got, []string{"t20", "t21"}) {
		t.Errorf("datamitsu-2.sarif = %v, want t20 and t21", got)
	}

	// Offline, the document's own tool count decides.
	e.wantExit(e.run("", nil, "lint", "--report", "json=run.json"), 0)
	e.wantExit(e.run("", nil, "report", "render", "--input", "run.json", "--format", "sarif"), 2)
	again := e.run("", nil, "report", "render", "--input", "run.json", "--format", "sarif", "--output", "again/")
	e.wantExit(again, 0)
	for _, name := range []string{"datamitsu-1.sarif", "datamitsu-2.sarif"} {
		if e.read(filepath.Join("again", name)) != e.read(filepath.Join("out", name)) {
			t.Errorf("report render wrote another %s than the run", name)
		}
	}
}

func TestReportSARIFRefusedAfterFix(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		toolExit     int
		wantExit     int
		platformSkip bool
		fits         bool
	}{
		{name: "export refused", output: "[]", wantExit: 5},
		{name: "tool failure wins", output: hadolintFinding, toolExit: 1, wantExit: 1},
		{name: "coverage failure wins", output: "[]", wantExit: 4, platformSkip: true},
		{name: "incomplete tool does not consume a slot", output: "[]", toolExit: 1, wantExit: 1},
		{name: "nineteen external tools and the built-in fit", output: "[]", toolExit: 1, wantExit: 1, fits: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{"fixture.marker": "", "a.txt": "fixture", ".datamitsuignore": "*.txt: t21\n"}, fixtureSpec)
			spec := fixtureSpec
			spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
			args := []string{"check", "--report", "sarif=r.sarif", "--report", "json=run.json"}
			if tc.platformSkip {
				spec.Extra = nativeSkipped()
				args = append(args, "--fail-on-skip")
			}
			n := 20
			if tc.fits {
				n = 19
			}
			tools := append(manyParsedTools(n),
				clitest.ShellTool("t21", fmt.Sprintf("%s; echo '%s'; exit %d", clitest.RecordRun, tc.output, tc.toolExit), clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"*.txt"}, Args: []string{"{file}"}, Parser: "hadolint"}),
				clitest.ShellTool("fixer", clitest.RecordRun+`; rm .datamitsuignore`, clitest.ToolOpSpec{Operation: "fix"}))
			e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tools...))
			e.p.WriteFile("r.sarif", `{"version":"2.1.0","runs":[]}`)
			res := e.run("", nil, jsonl(args...)...)
			e.wantExit(res, tc.wantExit)
			e.wantMarker("fixer", "fixer \n")
			events := clitest.MustParseJSONL(t, res.Stderr)
			reportEvents := func(format string) []clitest.Event {
				return eventsOf(events, func(e clitest.Event) bool { return e.Fields["format"] == format })
			}
			wantRunDone(t, events, "check", false, n+2, 0, true)
			if tc.platformSkip && runDone(t, events).Skipped != 1 {
				t.Fatal("run must count the unsupported lint tool")
			}
			_, doc := e.report("run.json")
			ops := operationsOf(t, doc)
			if lint := ops[1]; lint["ran"] != true || lint["success"] != (tc.toolExit == 0) {
				t.Fatalf("wrong lint outcome in JSON: ran=%v success=%v", lint["ran"], lint["success"])
			}
			wantReportEvent(t, reportEvents("json"), "json", "run.json", "written", "")
			want := []string{"datamitsu"}
			for i := 1; i <= n; i++ {
				want = append(want, fmt.Sprintf("t%02d", i))
			}
			if tc.fits {
				wantReportEvent(t, reportEvents("sarif"), "sarif", "r.sarif", "written", "")
				_, sarif := e.sarif("r.sarif")
				if !slices.Equal(sarif.tools(), want) || len(sarif.Runs[0].Results) != 0 {
					t.Fatalf("expected clean datamitsu and nineteen external tools, got %v", sarif.tools())
				}
				return
			}
			count := 22
			if tc.toolExit != 0 && tc.output == "[]" {
				count = 21
			}
			wantReportEvent(t, reportEvents("sarif"), "sarif", "r.sarif", "refused", fmt.Sprintf("%d tools", count))
			if _, err := os.Lstat(filepath.Join(e.p.Dir, "r.sarif")); !os.IsNotExist(err) {
				t.Fatalf("stale SARIF was not removed: %v", err)
			}
			for _, raw := range doc["exports"].([]any) {
				export := raw.(map[string]any)
				if export["format"] == "sarif" && (export["status"] != "refused" || export["omitted"] != nil) {
					t.Fatalf("refused SARIF has misleading export metadata: %v", export)
				}
			}
			if count == 21 {
				// Replay the live report, without fabricating an empty built-in run.
				single := e.run("", nil, "report", "render", "--input", "run.json", "--format", "sarif")
				e.wantExit(single, 2)
				if !strings.Contains(single.Stderr, "21 tools") || single.Stdout != "" {
					t.Fatalf("single export must count the clean built-in: %s\n%s", single.Stdout, single.Stderr)
				}
				e.wantExit(e.run("", nil, "report", "render", "--input", "run.json", "--format", "sarif", "--output", "split/"), 0)
				_, first := e.sarif("split/datamitsu-1.sarif")
				_, second := e.sarif("split/datamitsu-2.sarif")
				if len(first.Runs) != 20 || !slices.Equal(append(first.tools(), second.tools()...), want) || len(first.Runs[0].Results) != 0 {
					t.Fatalf("expected clean datamitsu and all complete tools split 20 + 1; got %v and %v", first.tools(), second.tools())
				}
			}
		})
	}
}

func TestReportSARIFRejectsReservedToolKey(t *testing.T) {
	for _, name := range []string{"datamitsu", "datamitsu-fix"} {
		t.Run(name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec)
			spec := fixtureSpec
			spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
			tool := clitest.ShellTool(name, passScript+"; echo '[]'", clitest.ToolOpSpec{Parser: "hadolint"})
			e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tool))
			res := e.run("", nil, "lint", "--report", "sarif=scan.sarif")
			e.wantExit(res, 1)
			if !strings.Contains(res.Stderr, fmt.Sprintf("tool %q: the key is reserved", name)) {
				t.Fatalf("expected a reserved tool key error, got:\n%s", res.Stderr)
			}
			e.wantMarker(name, "")
			if _, err := os.Lstat(filepath.Join(e.p.Dir, "scan.sarif")); !os.IsNotExist(err) {
				t.Fatalf("invalid configuration must not write SARIF: %v", err)
			}

			// The tool key changes; the app supplying the command does not.
			spec.Extra = fmt.Sprintf(`c.tools["external-check"] = c.tools[%q]; delete c.tools[%q];`, name, name) + "\n"
			e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tool))
			e.wantExit(e.run("", nil, "lint", "--report", "sarif=scan.sarif"), 0)
			if _, doc := e.sarif("scan.sarif"); !slices.Equal(doc.tools(), []string{"datamitsu", "external-check"}) {
				t.Fatalf("want distinct built-in and configured tool runs, got %v", doc.tools())
			}
		})
	}
}

func TestReportSARIFBuiltInFixHasSeparateDriver(t *testing.T) {
	for _, tc := range []struct{ name, lintOption, fixOption, lintID, fixID string }{
		{"same category", "", "", "datamitsu/", "datamitsu/"},
		{"overlapping category suffix", "?category=ci-fix", "?category=ci", "ci-fix/", "ci/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{
				"fixture.marker": "", ".datamitsuignore": "*.go:  *\n", "nested/.datamitsuignore": "*.go: *\n",
			}, fixtureSpec)
			e.wantExit(e.run("", nil, "lint", "--report", "sarif=lint.sarif"+tc.lintOption), 1)
			_, lint := e.sarif("lint.sarif")
			if !slices.Equal(lint.tools(), []string{"datamitsu"}) || lint.Runs[0].AutomationDetails.ID != tc.lintID {
				t.Fatalf("expected the built-in lint analysis: %+v", lint.Runs)
			}
			var placement bool
			for _, r := range lint.Runs[0].Results {
				placement = placement || r.RuleID == "config-placement"
			}
			if !placement {
				t.Fatalf("expected a placement finding in the lint analysis: %+v", lint.Runs)
			}
			e.wantExit(e.run("", nil, "fix", "--report", "sarif=fix.sarif"+tc.fixOption, "--report", "json=fix.json"), 0)
			_, fix := e.sarif("fix.sarif")
			if !slices.Equal(fix.tools(), []string{"datamitsu-fix"}) || fix.Runs[0].AutomationDetails.ID != tc.fixID ||
				len(fix.Runs[0].Results) != 1 || fix.Runs[0].Results[0].RuleID != "datamitsuignore-normalized" {
				t.Fatalf("expected a formatting finding in the separate fix analysis: %+v", fix.Runs)
			}
			if e.read("nested/.datamitsuignore") != "*.go: *\n" {
				t.Fatal("fix changed the nested file reported by lint")
			}
			replay := e.run("", nil, "report", "render", "--input", "fix.json", "--format", "sarif"+tc.fixOption)
			e.wantExit(replay, 0)
			if replay.Stdout != e.read("fix.sarif") {
				t.Fatalf("replay changed the fix analysis:\n%s", replay.Stdout)
			}
		})
	}
}

func TestReportSARIFRejectsLegacyReservedDriverKeys(t *testing.T) {
	for _, name := range []string{"datamitsu", "datamitsu-fix"} {
		t.Run(name, func(t *testing.T) {
			p := clitest.NewProject(t)
			p.WriteFile("old.json", strings.Replace(equivalentCompleteReport, `"ready"`, fmt.Sprintf("%q", name), 1))
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "report", "render", "--input", "old.json", "--format", "sarif")
			if res.ExitCode != 5 || res.Stdout != "" || !strings.Contains(res.Stderr, fmt.Sprintf("tool %q uses a reserved SARIF driver name", name)) {
				t.Fatalf("legacy report must not publish the reserved driver: exit=%d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
			}
		})
	}
}

func TestReportSARIFBuiltInFindingBecomesFixed(t *testing.T) {
	for _, setupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("external_setup_fails_%t", setupFails), func(t *testing.T) {
			e := newExecProject(t, map[string]string{"fixture.marker": "", ".datamitsuignore": "*.go: unknown-tool\n"}, fixtureSpec)
			e.wantExit(e.run("", nil, "lint", "--report", "json=before.json", "--report", "sarif=scan.sarif"), 1)
			_, before := e.sarif("scan.sarif")
			if !slices.Equal(before.tools(), []string{"datamitsu"}) || len(before.Runs[0].Results) != 1 || before.Runs[0].Results[0].RuleID != "datamitsuignore-tool" {
				t.Fatalf("expected a built-in finding in the first SARIF: %+v", before.Runs)
			}
			if err := os.Remove(filepath.Join(e.p.Dir, ".datamitsuignore")); err != nil {
				t.Fatal(err)
			}
			wantExit := 0
			if setupFails {
				spec := fixtureSpec
				spec.Extra = hostUninstallable("installer", "lint")
				e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec))
				wantExit = 1
			}
			res := e.run("", nil, "lint", "--report", "json=after.json", "--report", "sarif=scan.sarif")
			e.wantExit(res, wantExit)
			if setupFails && !strings.Contains(res.Stderr, "failed to pre-install tools") {
				t.Fatalf("expected the external setup error: %s", res.Stderr)
			}
			_, after := e.sarif("scan.sarif")
			if !slices.Equal(after.tools(), []string{"datamitsu"}) || len(after.Runs[0].Results) != 0 {
				t.Fatalf("expected a completed built-in run with no findings: %+v", after.Runs)
			}
			diff := e.run("", nil, "report", "diff", "before.json", "after.json", "--format", "json")
			e.wantExit(diff, 0)
			var result struct {
				Summary struct {
					Fixed      int `json:"fixed"`
					Unobserved int `json:"unobserved"`
				} `json:"summary"`
			}
			if err := json.Unmarshal([]byte(diff.Stdout), &result); err != nil || result.Summary.Fixed != 1 || result.Summary.Unobserved != 0 {
				t.Fatalf("expected the finding to be fixed: %v\n%s", err, diff.Stdout)
			}
		})
	}
}

func TestReportSARIFCountsPostFixCoreFinding(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	tools := append(manyParsedTools(20), clitest.ShellTool("fixer", `printf '*.go:t01\n' > .datamitsuignore`, clitest.ToolOpSpec{Operation: "fix"}))
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tools...))
	res := e.run("", nil, jsonl("check", "--events", "diagnostics=all", "--report", "sarif=r.sarif", "--report", "json=run.json")...)
	e.wantExit(res, 1)
	e.wantMarker("t01", "t01 \n")
	events := clitest.MustParseJSONL(t, res.Stderr)
	wantReportEvent(t, eventsOf(events, func(e clitest.Event) bool { return e.Fields["format"] == "sarif" }), "sarif", "r.sarif", "refused", "21 tools")
	diagnostics := diagnosticsOf(events)
	if len(diagnostics) != 1 || diagnostics[0].Fields["tool"] != "datamitsu" || diagnostics[0].Fields["code"] != "datamitsuignore-format" {
		t.Fatalf("expected the built-in diagnostic despite SARIF refusal: %+v", diagnostics)
	}
	_, doc := e.report("run.json")
	findings := findingsOf(t, doc)
	if len(findings) != 1 || findings[0]["code"] != "datamitsuignore-format" {
		t.Fatalf("expected the built-in finding in the JSON report: %v", findings)
	}
	wantRunDone(t, events, "check", false, 21, 0, true)
	console := e.run("", nil, "check", "--report", "sarif=r.sarif")
	e.wantExit(console, 1)
	if !strings.Contains(console.Stdout, "[datamitsuignore-format]") {
		t.Fatalf("expected the built-in finding in the terminal:\n%s", console.Stdout)
	}
}

func TestReportSARIFRefusedAfterBuiltInFix(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": "", ".datamitsuignore": "*.go: t01  \n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	tools := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		tools = append(tools, clitest.ShellTool(fmt.Sprintf("t%02d", i), clitest.RecordRun+`; echo '[]'`, clitest.ToolOpSpec{Operation: "fix", Parser: "hadolint"}))
	}
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tools...))
	res := e.run("", nil, jsonl("fix", "--report", "sarif=r.sarif")...)
	e.wantExit(res, 5)
	e.wantMarker("t01", "t01 \n")
	if got := e.read(".datamitsuignore"); got != "*.go: t01\n" {
		t.Fatalf("expected bundled fix before capacity refusal, got %q", got)
	}
	wantReportEvent(t, clitest.MustParseJSONL(t, res.Stderr), "sarif", "r.sarif", "refused", "21 tools")
}

func TestPostFixLintSetupFailureKeepsCoreDiagnostic(t *testing.T) {
	spec := fixtureSpec
	spec.Extra = hostUninstallable("installer", "lint")
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, spec,
		clitest.ShellTool("fixer", `printf '*.go:installer\n' > .datamitsuignore`, clitest.ToolOpSpec{Operation: "fix"}))
	res := e.run("", nil, jsonl("check", "--events", "diagnostics=all")...)
	e.wantExit(res, 1)
	events := clitest.MustParseJSONL(t, res.Stderr)
	diags := diagnosticsOf(events)
	if len(diags) != 1 || diags[0].Fields["code"] != "datamitsuignore-format" || len(toolRuns(events, "installer")) != 0 {
		t.Fatalf("expected the bundled finding and no external lint run: %+v", events)
	}
	wantRunDone(t, events, "check", false, 1, 0, false)
	console := e.run("", nil, "check")
	e.wantExit(console, 1)
	if !strings.Contains(console.Stdout, "[datamitsuignore-format]") || !strings.Contains(console.Stderr, "failed to pre-install tools") {
		t.Fatalf("expected both the core diagnostic and setup error:\n%s\n%s", console.Stdout, console.Stderr)
	}
}

func TestStandaloneLintSetupFailureKeepsPlacementDiagnostic(t *testing.T) {
	spec := fixtureSpec
	spec.Extra = hostUninstallable("installer", "lint")
	e := newExecProject(t, map[string]string{"fixture.marker": "", "nested/.datamitsuignore": "*.go: installer\n"}, spec)
	for _, command := range []string{"lint", "check"} {
		t.Run(command, func(t *testing.T) {
			res := e.run("", nil, jsonl(command, "--events", "diagnostics=all")...)
			e.wantExit(res, 1)
			events := clitest.MustParseJSONL(t, res.Stderr)
			diags := diagnosticsOf(events)
			if len(diags) != 1 || diags[0].Fields["code"] != "config-placement" || len(toolRuns(events, "installer")) != 0 {
				t.Fatalf("expected a placement diagnostic and no external lint run: %+v", events)
			}
			wantRunDone(t, events, command, false, 0, 0, false)
			console := e.run("", nil, command)
			e.wantExit(console, 1)
			if !strings.Contains(console.Stdout, "[config-placement]") || !strings.Contains(console.Stderr, "failed to pre-install tools") {
				t.Fatalf("expected both placement and setup errors:\n%s\n%s", console.Stdout, console.Stderr)
			}
		})
	}
}

// findingsOf lists the findings of a decoded own JSON document.
func findingsOf(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, op := range operationsOf(t, doc) {
		tools, _ := op["tools"].([]any)
		for _, tool := range tools {
			invocations, _ := tool.(map[string]any)["invocations"].([]any)
			for _, inv := range invocations {
				findings, _ := inv.(map[string]any)["findings"].([]any)
				for _, f := range findings {
					out = append(out, f.(map[string]any))
				}
			}
		}
	}
	return out
}
