package cli_test

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes --report checkstyle and --report rdjsonl: every finding of
// the lint operation, a finding without a file included, and the completeness
// companion beside each.
func TestReportCheckstyleAndRdjsonl(t *testing.T) {
	e := reportProject(t, outsider, clitest.ShellTool("beta", failScript, clitest.ToolOpSpec{}))
	res := e.run("", nil, "lint", "--report", "checkstyle=out/checkstyle.xml", "--report", "rdjsonl=out/rd.jsonl")
	e.wantExit(res, 1)

	checkstyle := e.read("out/checkstyle.xml")
	var doc struct {
		XMLName xml.Name `xml:"checkstyle"`
	}
	if err := xml.Unmarshal([]byte(checkstyle), &doc); err != nil {
		t.Fatalf("checkstyle is not well-formed XML: %v\n%s", err, checkstyle)
	}
	for _, want := range []string{
		`<file name="/outside/Dockerfile">`,
		`<file name="">`,
		`severity="error" message="beta exited 1 without parsable findings" source="beta"`,
	} {
		if !strings.Contains(checkstyle, want) {
			t.Errorf("checkstyle lacks %s:\n%s", want, checkstyle)
		}
	}

	stream := e.read("out/rd.jsonl")
	lines := strings.Split(strings.TrimSuffix(stream, "\n"), "\n")
	for _, line := range lines {
		var d map[string]any
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("an rdjsonl line is not JSON: %v\n%s", err, line)
		}
	}
	if len(lines) != 3 || !strings.Contains(lines[2], `"message":"beta exited 1 without parsable findings"`) || strings.Contains(lines[2], `"location"`) {
		t.Errorf("rdjsonl = %s, want three lines, the last one without a location", stream)
	}

	for _, rel := range []string{"out/checkstyle.xml", "out/rd.jsonl"} {
		if _, decoded := e.companion(rel); decoded["complete"] != false {
			t.Errorf("the companion of %s says complete: alpha and beta have no parser", rel)
		}
	}
	if !strings.Contains(res.Stderr, "WARN report: lint tool beta is incomplete (no-extraction): flagged in the completeness companion of checkstyle, rdjsonl") {
		t.Errorf("stderr:\n%s", res.Stderr)
	}
	e.goldenReport("checkstyle_lint", e.normalize(checkstyle))
	e.goldenReport("rdjsonl_lint", e.normalize(stream))

	// Both list findings: a narrowed run is refused.
	refused := e.run("", nil, "lint", "Dockerfile", "--report", "rdjsonl=narrowed.jsonl")
	e.wantExit(refused, 2)
}
