package cli_test

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes --report junit: which cases fail, and the completeness
// companion written beside the document.

// junitTimeRE masks what a document measured: the durations are real.
var junitTimeRE = regexp.MustCompile(`time="[0-9]+\.[0-9]{3}"`)

func (e *execProject) junit(rel string) string {
	e.t.Helper()
	raw := e.read(rel)
	var doc struct {
		XMLName xml.Name `xml:"testsuites"`
	}
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		e.t.Fatalf("%s is not well-formed XML: %v\n%s", rel, err, raw)
	}
	return e.normalize(junitTimeRE.ReplaceAllString(raw, `time="<DUR>"`))
}

// companion reads the completeness companion of the report at rel.
func (e *execProject) companion(rel string) (string, map[string]any) {
	e.t.Helper()
	raw := e.read(rel + ".completeness.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		e.t.Fatalf("the companion of %s is not JSON: %v\n%s", rel, err, raw)
	}
	return e.normalize(raw), doc
}

// TestReportJUnit: a case per file a tool answered for, failing only where a
// finding gated; a tool that failed without a finding is one error case with
// its output; the companion says which tools are incomplete.
func TestReportJUnit(t *testing.T) {
	t.Run("lint", func(t *testing.T) {
		e := reportProject(t, clitest.ShellTool("beta", failScript, clitest.ToolOpSpec{}))
		res := e.run("", nil, "lint", "--report", "junit=out/junit.xml")
		e.wantExit(res, 1)
		doc := e.junit("out/junit.xml")
		for _, want := range []string{
			`<testsuite name="lint/hadolint" tests="1" failures="0" errors="0" skipped="0"`,
			`<system-out>Dockerfile:1:1: warning hadolint(DL3006): Always tag the version of an image explicitly</system-out>`,
			`<testsuite name="lint/beta" tests="4" failures="0" errors="1" skipped="0"`,
			`<error type="exit" message="exit 1">beta exited 1 without parsable findings`,
		} {
			if !strings.Contains(doc, want) {
				t.Errorf("the document lacks %s:\n%s", want, doc)
			}
		}
		companion, decoded := e.companion("out/junit.xml")
		if decoded["complete"] != false || decoded["schema"] != "datamitsu.completeness/1" {
			t.Errorf("companion = %s", companion)
		}
		for _, tool := range []string{"alpha", "beta"} {
			if !strings.Contains(res.Stderr, "WARN report: lint tool "+tool+" is incomplete (no-extraction): flagged in the completeness companion of junit") {
				t.Errorf("stderr does not flag %s:\n%s", tool, res.Stderr)
			}
		}
		e.goldenReport("junit_lint", doc)
		e.goldenReport("junit_lint_companion", companion)
	})

	t.Run("check", func(t *testing.T) {
		e := reportProject(t, clitest.ShellTool("gamma", passScript, clitest.ToolOpSpec{Operation: "fix"}))
		res := e.run("", nil, "check", "--report", "junit=junit.xml")
		e.wantExit(res, 0)
		doc := e.junit("junit.xml")
		for _, want := range []string{`<testsuite name="fix/gamma"`, `<testsuite name="lint/alpha"`, `<testsuite name="lint/hadolint"`} {
			if !strings.Contains(doc, want) {
				t.Errorf("the document lacks %s:\n%s", want, doc)
			}
		}
	})

	// A narrowed run is refused, as for every report that lists findings;
	// --allow-partial writes it, with the reasons in the companion.
	t.Run("narrowed", func(t *testing.T) {
		e := reportProject(t)
		e.wantExit(e.run("", nil, "lint", "Dockerfile", "--report", "junit=junit.xml"), 2)
		if _, err := os.Stat(filepath.Join(e.p.Dir, "junit.xml")); err == nil {
			t.Error("a refused run wrote the report")
		}
		e.wantExit(e.run("", nil, "lint", "Dockerfile", "--report", "junit=junit.xml", "--allow-partial"), 0)
		companion, decoded := e.companion("junit.xml")
		if decoded["complete"] != false || !strings.Contains(companion, `"narrowed-selection"`) {
			t.Errorf("companion = %s", companion)
		}
	})

	// On stdout there is no companion, and the warning says so.
	t.Run("stdout", func(t *testing.T) {
		e := reportProject(t)
		res := e.run("", nil, "lint", "--report", "junit=-")
		e.wantExit(res, 0)
		var doc struct {
			XMLName xml.Name `xml:"testsuites"`
		}
		if err := xml.Unmarshal([]byte(res.Stdout), &doc); err != nil {
			t.Fatalf("stdout is not the document alone: %v\n%s", err, res.Stdout)
		}
		if !strings.Contains(res.Stderr, "listed as if complete by junit on stdout, which has no companion") {
			t.Errorf("stderr:\n%s", res.Stderr)
		}
	})
}
