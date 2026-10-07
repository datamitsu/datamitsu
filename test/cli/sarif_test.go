package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/report"
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
		if got := doc.tools(); len(got) != 1 || got[0] != "hadolint" {
			t.Fatalf("runs = %v, want hadolint alone: alpha has no parser, so it is never complete", got)
		}
		run := doc.Runs[0]
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
		if _, doc := e.sarif("r.sarif"); strings.Join(doc.tools(), ",") != "hadolint" {
			t.Errorf("runs = %v, want the lint operation's hadolint alone", doc.tools())
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

	// A parsed tool that failed while its parser found nothing in what it
	// printed is not complete: a run of it without results would close every
	// alert it has.
	t.Run("failed_without_findings", func(t *testing.T) {
		e := sarifProject(t, clitest.ShellTool("crasher", settle+clitest.RecordRun+"; echo 'panic: not a finding'; exit 2",
			clitest.ToolOpSpec{Parser: "hadolint"}))
		res := e.run("", nil, "lint", "--report", "sarif=r.sarif", "--report", "json=run.json")
		e.wantExit(res, 1)
		if _, doc := e.sarif("r.sarif"); strings.Join(doc.tools(), ",") != "hadolint" {
			t.Errorf("runs = %v, want hadolint alone", doc.tools())
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
		old, err := json.MarshalIndent(&run, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		e.p.WriteFile("old.json", string(old))
		rendered := e.run("", nil, "report", "render", "--input", "old.json", "--format", "sarif")
		e.wantExit(rendered, 0)
		if strings.Contains(rendered.Stdout, `"name": "crasher"`) {
			t.Errorf("an earlier build's document wrote crasher's run:\n%s", rendered.Stdout)
		}
	})

	// --tools narrows the run, not the selected tools' own runs: a selected
	// tool that is complete is written, and the others' alerts are left alone.
	t.Run("tools", func(t *testing.T) {
		e := sarifProject(t)
		res := e.run("", nil, "lint", "--tools", "hadolint", "--report", "sarif=r.sarif")
		e.wantExit(res, 0)
		if _, doc := e.sarif("r.sarif"); strings.Join(doc.tools(), ",") != "hadolint" {
			t.Errorf("runs = %v, want the selected hadolint", doc.tools())
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
	if got := first.tools(); len(got) != 20 || got[0] != "t01" || got[19] != "t20" {
		t.Errorf("datamitsu-1.sarif = %v, want t01…t20", got)
	}
	if got := second.tools(); strings.Join(got, ",") != "t21" {
		t.Errorf("datamitsu-2.sarif = %v, want t21", got)
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
