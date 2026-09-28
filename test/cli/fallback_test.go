package cli_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// sarifLog is what ruff prints with --output-format sarif, trimmed.
const sarifLog = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"ruff"}},"results":[{"ruleId":"F401",` +
	`"level":"error","message":{"text":"os imported but unused"},"locations":[{"physicalLocation":` +
	`{"artifactLocation":{"uri":"a.py"},"region":{"startLine":1,"startColumn":8}}}]}]}]}`

// fallbackProject is a repository whose tools the scenario declares, with the
// committed parser module seeded as "core".
func fallbackProject(t *testing.T, files map[string]string, tools ...string) *execProject {
	t.Helper()
	all := map[string]string{"fixture.marker": ""}
	maps.Copy(all, files)
	e := newExecProject(t, all, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tools...))
	return e
}

// TestFallbackReadsAToolWithoutAParser: a tool with no outputParser that
// prints SARIF has its findings read by the fallback built into the binary,
// shown in its frame and reported with the fallback:sarif provenance; with
// --no-parse the frame shows what it printed.
func TestFallbackReadsAToolWithoutAParser(t *testing.T) {
	ruff := clitest.ShellTool("ruff", settle+clitest.RecordRun+"; printf '%s\\n' '"+sarifLog+"'; exit 1", clitest.ToolOpSpec{})
	e := fallbackProject(t, map[string]string{"a.py": "import os\n"}, ruff)

	res := e.run("", nil, "lint", "--report", "json=run.json")
	e.wantExit(res, 1)
	if !strings.Contains(res.Stdout, "a.py:1:8 error os imported but unused [F401]") {
		t.Errorf("the frame should show the finding the fallback read:\n%s", res.Stdout)
	}
	doc := e.read("run.json")
	for _, want := range []string{`"provenance": "fallback:sarif"`, `"extraction": "parsed-findings"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("run.json lacks %s:\n%s", want, doc)
		}
	}
	e.golden("fallback_sarif", res)

	raw := e.run("", nil, "lint", "--no-parse")
	e.wantExit(raw, 1)
	if !strings.Contains(raw.Stdout, `"ruleId":"F401"`) {
		t.Errorf("--no-parse should frame what the tool printed:\n%s", raw.Stdout)
	}
}

// TestFallbackReadsTheStreamsApart: a tool without a parser that writes
// progress to stderr while its SARIF goes to stdout keeps the document whole.
func TestFallbackReadsTheStreamsApart(t *testing.T) {
	half := len(sarifLog) / 2
	script := settle + clitest.RecordRun + "; echo 'scanning' >&2; printf '%s' '" + sarifLog[:half] +
		"'; echo 'still scanning' >&2; printf '%s\\n' '" + sarifLog[half:] + "'; exit 1"
	e := fallbackProject(t, map[string]string{"a.py": "import os\n"}, clitest.ShellTool("ruff", script, clitest.ToolOpSpec{}))

	res := e.run("", nil, "lint", "--report", "json=run.json")
	e.wantExit(res, 1)
	if doc := e.read("run.json"); !strings.Contains(doc, `"provenance": "fallback:sarif"`) {
		t.Errorf("the SARIF was not read apart from the progress on stderr:\n%s\n%s", doc, res.Stdout)
	}
}

// TestACutOffDocumentIsNotACleanRun: a tool without a parser that exits 0
// printing a SARIF log cut off before it closes is truncated, not clean: the
// report marks it incomplete, and the next run runs it again.
func TestACutOffDocumentIsNotACleanRun(t *testing.T) {
	cut := sarifLog[:len(sarifLog)/2]
	ruff := clitest.ShellTool("ruff", settle+clitest.RecordRun+"; printf '%s' '"+cut+"'", clitest.ToolOpSpec{})
	e := fallbackProject(t, map[string]string{"a.py": "import os\n"}, ruff)

	res := e.run("", nil, "lint", "--report", "json=run.json")
	e.wantExit(res, 0)
	doc := e.read("run.json")
	for _, want := range []string{`"extraction": "truncated"`, `"complete": false`} {
		if !strings.Contains(doc, want) {
			t.Errorf("run.json lacks %s:\n%s", want, doc)
		}
	}
	e.wantExit(e.run("", nil, "lint"), 0)
	if _, got := e.p.Marker("ruff"); strings.Count(got, "ruff") != 2 {
		t.Errorf("ruff ran %q, want twice: no pass stands for a cut-off document", got)
	}
}

// TestFallbackStandsInForADeclaredParser: a declared parser that does not
// recognize the output hands it to the fallback, and the run says so once.
func TestFallbackStandsInForADeclaredParser(t *testing.T) {
	cc := clitest.ShellTool("cc", settle+clitest.RecordRun+"; echo 'a.c:1:2: error: y undeclared'; exit 1",
		clitest.ToolOpSpec{Parser: "hadolint"})
	e := fallbackProject(t, map[string]string{"a.c": "int x;\n"}, cc)

	res := e.run("", nil, "lint", "--report", "json=run.json")
	e.wantExit(res, 1)
	want := `declared parser "hadolint" of module "core" did not recognize the output of cc; the fallback parsed it as gcc`
	if n := strings.Count(res.Stderr, want); n != 1 {
		t.Errorf("stderr carries %q %d times, want once:\n%s", want, n, res.Stderr)
	}
	if doc := e.read("run.json"); !strings.Contains(doc, `"provenance": "fallback:gcc"`) {
		t.Errorf("run.json lacks the fallback provenance:\n%s", doc)
	}
	e.golden("fallback_declared", res)
}

// TestADeclaredParserThatRecognizesNothingFailsTheParse: when neither the
// declared parser nor any standard format recognizes a tool's output, the
// output is parse-failed: the tool is incomplete, and the run says why.
func TestADeclaredParserThatRecognizesNothingFailsTheParse(t *testing.T) {
	crasher := clitest.ShellTool("crasher", settle+clitest.RecordRun+"; echo 'panic: not a finding'; exit 2",
		clitest.ToolOpSpec{Parser: "hadolint"})
	e := fallbackProject(t, nil, crasher)

	res := e.run("", nil, "lint", "--report", "json=run.json")
	e.wantExit(res, 1)
	if !strings.Contains(res.Stderr, `declared parser "hadolint" of module "core" did not recognize the output of crasher, nor did any standard format`) {
		t.Errorf("stderr:\n%s", res.Stderr)
	}
	doc := e.read("run.json")
	for _, want := range []string{`"extraction": "parse-failed"`, `"parse-failed"`, `"complete": false`} {
		if !strings.Contains(doc, want) {
			t.Errorf("run.json lacks %s:\n%s", want, doc)
		}
	}
	e.golden("fallback_parse_failed", res)
}

// TestOutputNothingRecognizesStandsForItself: a tool without a parser that
// fails printing nothing the fallback reads keeps the exit-status rule — a
// synthetic finding in the report — and a compiler line naming a file that
// does not exist is prose, not a finding.
func TestOutputNothingRecognizesStandsForItself(t *testing.T) {
	prose := clitest.ShellTool("prose", settle+clitest.RecordRun+"; echo 'nowhere.c:1:2: error: y'; exit 1", clitest.ToolOpSpec{})
	e := fallbackProject(t, nil, prose)

	res := e.run("", nil, "lint", "--report", "json=run.json")
	e.wantExit(res, 1)
	doc := e.read("run.json")
	for _, want := range []string{`"kind": "synthetic"`, `"extraction": "none"`, `"no-extraction"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("run.json lacks %s:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, `"provenance": "fallback:`) {
		t.Errorf("a line naming no file was read as a finding:\n%s", doc)
	}
	if !strings.Contains(res.Stdout, "nowhere.c:1:2: error: y") {
		t.Errorf("the frame should show what the tool printed:\n%s", res.Stdout)
	}
}

// TestAFormattersOutputIsNeverAFinding: a stdout-mode formatter prints the
// file's new content, which no parser reads, whatever it looks like.
func TestAFormattersOutputIsNeverAFinding(t *testing.T) {
	formatter := clitest.ShellTool("formatter", settle+clitest.RecordRun+`; printf 'package x\n// x.go:1:2: error: y\n'`,
		clitest.ToolOpSpec{
			Operation: "fix", Scope: "per-file", Globs: []string{"**/*.go"}, Args: []string{"{file}"},
			Input: "stdin", Output: "stdout",
		})
	e := fallbackProject(t, map[string]string{"x.go": "package x\n"}, formatter)

	res := e.run("", nil, "fix", "--report", "json=run.json")
	e.wantExit(res, 0)
	if got := e.read("x.go"); got != "package x\n// x.go:1:2: error: y\n" {
		t.Errorf("x.go = %q, want the formatted content", got)
	}
	doc := e.read("run.json")
	if strings.Contains(doc, `"provenance": "fallback:`) || !strings.Contains(doc, `"findings": []`) {
		t.Errorf("the formatted content was read as a finding:\n%s", doc)
	}
}
