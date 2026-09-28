package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes report baseline, --baseline and report diff: a baseline
// silences the findings it holds in the gate, never an exit code, and a diff
// calls a finding fixed only where the second run looked.

// gatedProject lints the Dockerfile at priority 10, prints output and exits with
// exitCode; beta runs after it at priority 20, so fail-fast after hadolint
// leaves beta unstarted.
func gatedProject(t *testing.T, output string, exitCode int) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	hadolint := clitest.ShellTool("hadolint",
		fmt.Sprintf("%s%s; echo '%s'; exit %d", settle, clitest.RecordRun, output, exitCode),
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/Dockerfile"}, Args: []string{"{file}"}, Parser: "hadolint", Priority: 10})
	beta := clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{Priority: 20})
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, hadolint, beta))
	return e
}

// baselineOf writes the baseline of a lint run of e to out/base.json.
func (e *execProject) baselineOf(extra ...string) {
	e.t.Helper()
	args := append([]string{"lint", "--report", "json=out/run.json"}, extra...)
	e.run("", nil, args...)
	e.wantExit(e.run("", nil, "report", "baseline", "out/run.json", "--output", "out/base.json"), 0)
}

func (e *execProject) remove(rel string) {
	e.t.Helper()
	if err := os.Remove(filepath.Join(e.p.Dir, rel)); err != nil {
		e.t.Fatal(err)
	}
}

// TestReportBaseline: report baseline followed by lint --baseline silences
// the baselined finding and does not cancel the next task under fail-fast,
// but a tool that exits non-zero still fails the run.
func TestReportBaseline(t *testing.T) {
	t.Run("silences", func(t *testing.T) {
		e := gatedProject(t, hadolintError, 0)
		res := e.run("", nil, "lint")
		e.wantExit(res, 1)
		e.wantMarker("beta", "")

		e.baselineOf()
		var doc map[string]any
		if err := json.Unmarshal([]byte(e.read("out/base.json")), &doc); err != nil {
			t.Fatal(err)
		}
		if doc["schema"] != "datamitsu.baseline/1" || doc["fingerprint"] != "dmfp1" || len(doc["fingerprints"].([]any)) != 1 {
			t.Fatalf("baseline = %v", doc)
		}
		clitest.AssertGolden(t, "report_baseline_document", e.normalize(e.read("out/base.json")))
		e.remove("out/run.json")
		e.remove(".markers/beta")

		res = e.run("", nil, "lint", "--baseline", "out/base.json")
		e.wantExit(res, 0)
		e.wantMarker("beta", "beta \n")
		if !strings.Contains(res.Stdout, "· 1 baselined") {
			t.Errorf("the baselined finding should be counted:\n%s", res.Stdout)
		}
		e.golden("baseline_lint", res)
	})

	// A report of the run marks the finding and the diagnostic event carries
	// the flag; the event stream does not emit it by default, as it is not
	// reported.
	t.Run("report", func(t *testing.T) {
		e := gatedProject(t, hadolintError, 0)
		e.baselineOf()
		res := e.run("", nil, "lint", "--baseline", "out/base.json", "--report", "json=out/again.json", "--report", "sarif=out/r.sarif")
		e.wantExit(res, 0)
		again := e.read("out/again.json")
		if !strings.Contains(again, `"baselined": true`) || !strings.Contains(again, `"gates": false`) {
			t.Errorf("the finding should be baselined and not gate:\n%s", again)
		}
		if !strings.Contains(e.read("out/r.sarif"), `"baselined": true`) {
			t.Errorf("the SARIF result should carry properties.baselined")
		}
	})

	// A baseline cannot silence an exit code: most linters exit 1 on what
	// they print.
	t.Run("exit_code", func(t *testing.T) {
		e := gatedProject(t, hadolintError, 1)
		e.baselineOf()
		res := e.run("", nil, "lint", "--baseline", "out/base.json")
		e.wantExit(res, 1)
		if !strings.Contains(res.Stdout, "(baselined)") {
			t.Errorf("a failed frame shows the baselined findings it failed on, marked:\n%s", res.Stdout)
		}
	})

	// The own report of a run serves as a baseline too.
	t.Run("from_report", func(t *testing.T) {
		e := gatedProject(t, hadolintError, 0)
		e.run("", nil, "lint", "--report", "json=out/run.json")
		e.wantExit(e.run("", nil, "lint", "--baseline", "out/run.json"), 0)
	})

	// A baseline of an incomplete run is written, with a warning.
	t.Run("incomplete_source", func(t *testing.T) {
		e := gatedProject(t, hadolintError, 0)
		e.run("", nil, "lint", "--report", "json=out/run.json", "--allow-partial", "Dockerfile")
		res := e.run("", nil, "report", "baseline", "out/run.json")
		e.wantExit(res, 0)
		if !strings.Contains(res.Stderr, "is incomplete (") || !strings.Contains(res.Stdout, `"datamitsu.baseline/1"`) {
			t.Errorf("want the baseline on stdout and a warning:\n%s\n%s", res.Stdout, res.Stderr)
		}
		e.golden("baseline_incomplete_source", res)
	})

	// So does an incomplete own report given as the baseline: it warns and
	// works.
	t.Run("incomplete_report_as_baseline", func(t *testing.T) {
		e := gatedProject(t, hadolintError, 0)
		e.run("", nil, "lint", "--report", "json=out/run.json", "--allow-partial", "Dockerfile")
		res := e.run("", nil, "lint", "--baseline", "out/run.json")
		e.wantExit(res, 0)
		if !strings.Contains(res.Stderr, "--baseline: the run of out/run.json is incomplete (") ||
			!strings.Contains(res.Stdout, "· 1 baselined") {
			t.Errorf("want a warning and the finding baselined:\n%s\n%s", res.Stdout, res.Stderr)
		}
	})

	t.Run("refused", func(t *testing.T) {
		e := gatedProject(t, hadolintError, 0)
		e.p.WriteFile("foreign.json", `{"schema":"datamitsu.baseline/2","fingerprint":"dmfp1","fingerprints":[]}`)
		e.p.WriteFile("version.json", `{"schema":"datamitsu.baseline/1","fingerprint":"dmfp2","fingerprints":[]}`)
		for _, name := range []string{"foreign.json", "version.json", "missing.json"} {
			res := e.run("", nil, "lint", "--baseline", name)
			e.wantExit(res, 2)
			e.wantMarker("hadolint", "")
			if name == "version.json" {
				e.golden("baseline_refused_version", res)
			}
		}
		e.wantExit(e.run("", nil, "report", "baseline"), 2)
		e.wantExit(e.run("", nil, "report", "baseline", "missing.json"), 1)

		// A report of fingerprints of another version is neither a baseline
		// nor made into one.
		e.run("", nil, "lint", "--report", "json=out/run.json")
		v2 := strings.Replace(e.read("out/run.json"), `"fingerprint": "dmfp1"`, `"fingerprint": "dmfp2"`, 1)
		if !strings.Contains(v2, `"dmfp2"`) {
			t.Fatalf("the report states no fingerprint version:\n%s", v2)
		}
		e.p.WriteFile("out/v2.json", v2)
		e.wantExit(e.run("", nil, "report", "baseline", "out/v2.json"), 2)
		e.wantExit(e.run("", nil, "lint", "--baseline", "out/v2.json"), 2)
	})
}

// diffProject is reportProject whose parsed tool prints what the ignored file
// .markers/findings holds, so that a scenario changes what it finds between
// two runs.
func diffProject(t *testing.T, findings string) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	hadolint := clitest.ShellTool("hadolint", settle+clitest.RecordRun+`; cat "$MARKERS/findings"`,
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/Dockerfile"}, Args: []string{"{file}"}, Parser: "hadolint"})
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}), hadolint))
	e.p.WriteFile(filepath.Join(clitest.MarkerDirName, "findings"), findings)
	return e
}

// TestReportDiff: diffing a run with a finding against one where it was fixed
// lists exactly one fixed finding, and a narrowed second run cannot call it
// fixed.
func TestReportDiff(t *testing.T) {
	t.Run("fixed", func(t *testing.T) {
		e := diffProject(t, hadolintFinding)
		e.wantExit(e.run("", nil, "lint", "--report", "json=before.json"), 0)
		e.p.WriteFile(filepath.Join(clitest.MarkerDirName, "findings"), "[]")
		e.wantExit(e.run("", nil, "lint", "--report", "json=after.json"), 0)

		res := e.run("", nil, "report", "diff", "before.json", "after.json")
		e.wantExit(res, 0)
		var doc struct {
			Summary map[string]int `json:"summary"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
			t.Fatalf("stdout is not the diff: %v\n%s", err, res.Stdout)
		}
		want := map[string]int{"new": 0, "fixed": 1, "unchanged": 0, "moved": 0, "unknown": 0, "unobserved": 0}
		if fmt.Sprint(doc.Summary) != fmt.Sprint(want) {
			t.Errorf("summary = %v, want %v", doc.Summary, want)
		}
		e.golden("report_diff_fixed", res)

		res = e.run("", nil, "report", "diff", "after.json", "before.json", "--format", "markdown")
		e.wantExit(res, 0)
		if !strings.Contains(res.Stdout, "1 new · 0 fixed") {
			t.Errorf("the other direction should call it new:\n%s", res.Stdout)
		}
		e.golden("report_diff_markdown", res)
	})

	t.Run("narrowed_after", func(t *testing.T) {
		e := diffProject(t, hadolintFinding)
		e.wantExit(e.run("", nil, "lint", "--report", "json=before.json"), 0)
		e.p.WriteFile(filepath.Join(clitest.MarkerDirName, "findings"), "[]")
		e.wantExit(e.run("", nil, "lint", "--report", "json=after.json", "--allow-partial", "Dockerfile"), 0)
		res := e.run("", nil, "report", "diff", "before.json", "after.json")
		e.wantExit(res, 0)
		if !strings.Contains(res.Stdout, `"unknown": 1`) || !strings.Contains(res.Stdout, `"narrowed-selection"`) {
			t.Errorf("the finding should be unknown, with the reason:\n%s", res.Stdout)
		}
	})

	t.Run("usage", func(t *testing.T) {
		e := reportProject(t)
		e.wantExit(e.run("", nil, "lint", "--report", "json=before.json"), 0)
		e.wantExit(e.run("", nil, "report", "diff", "before.json"), 2)
		e.wantExit(e.run("", nil, "report", "diff", "before.json", "before.json", "--format", "yaml"), 2)
		e.wantExit(e.run("", nil, "report", "diff", "before.json", "missing.json"), 1)
		res := e.run("", nil, "report", "diff", "before.json", "before.json", "--output", "out/diff.json")
		e.wantExit(res, 0)
		if !strings.Contains(e.read("out/diff.json"), `"unchanged": 1`) {
			t.Errorf("a report diffed with itself is unchanged:\n%s", e.read("out/diff.json"))
		}
	})
}
