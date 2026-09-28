package junit

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

// The decoded shape a JUnit consumer reads: the tests assert through it that
// the document is well-formed XML and what each case says.
type xmlSuites struct {
	XMLName  xml.Name   `xml:"testsuites"`
	Name     string     `xml:"name,attr"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Errors   int        `xml:"errors,attr"`
	Skipped  int        `xml:"skipped,attr"`
	Suites   []xmlSuite `xml:"testsuite"`
}

type xmlSuite struct {
	Name       string        `xml:"name,attr"`
	Tests      int           `xml:"tests,attr"`
	Failures   int           `xml:"failures,attr"`
	Errors     int           `xml:"errors,attr"`
	Skipped    int           `xml:"skipped,attr"`
	Time       string        `xml:"time,attr"`
	Timestamp  string        `xml:"timestamp,attr"`
	Properties []xmlProperty `xml:"properties>property"`
	Cases      []xmlCase     `xml:"testcase"`
}

type xmlProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlCase struct {
	ClassName string      `xml:"classname,attr"`
	Name      string      `xml:"name,attr"`
	Time      string      `xml:"time,attr"`
	Failure   *xmlMessage `xml:"failure"`
	Error     *xmlMessage `xml:"error"`
	Skipped   *xmlMessage `xml:"skipped"`
	SystemOut string      `xml:"system-out"`
}

type xmlMessage struct {
	Type    string `xml:"type,attr"`
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

func decode(t *testing.T, run *report.Run) (xmlSuites, string) {
	t.Helper()
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, run, nil); err != nil {
		t.Fatal(err)
	}
	var doc xmlSuites
	if err := xml.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("not well-formed XML: %v\n%s", err, b.String())
	}
	return doc, b.String()
}

func (s xmlSuites) suite(t *testing.T, name string) xmlSuite {
	t.Helper()
	for _, suite := range s.Suites {
		if suite.Name == name {
			return suite
		}
	}
	t.Fatalf("no suite %s among %+v", name, s.Suites)
	return xmlSuite{}
}

func (s xmlSuite) testCase(t *testing.T, name string) xmlCase {
	t.Helper()
	for _, c := range s.Cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("suite %s has no case %s: %+v", s.Name, name, s.Cases)
	return xmlCase{}
}

func (s xmlSuite) property(name string) string {
	for _, p := range s.Properties {
		if p.Name == name {
			return p.Value
		}
	}
	return "<none>"
}

func issue(path string, row int, severity string, gates bool, msg string) report.Finding {
	return report.Finding{
		Tool: "eslint", Source: "eslint", Code: "no-var", Severity: severity, Gates: gates, Reported: gates, Kind: "issue", Message: msg,
		Location: report.Location{Path: path, Row: row, EndRow: row, Chars: &textpos.Span{Start: 3, End: 6}, Precision: "exact"},
	}
}

func files(paths ...string) []report.FileResult {
	out := make([]report.FileResult, len(paths))
	for i, p := range paths {
		out[i] = report.FileResult{Path: p, State: "ran"}
	}
	return out
}

func sampleRun() *report.Run {
	one, two := 1, 2
	return &report.Run{
		StartedAt: time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC),
		Selection: report.Selection{Mode: "all"},
		Operations: []report.Operation{
			{
				Name: "lint", Ran: true,
				Skipped:   []report.Skip{{Tool: "trivy", Reason: "config", Detail: "runs in CI only"}},
				Cancelled: []report.Cancel{{TaskID: "knip:pkg:5", Tool: "knip", Dir: "pkg", Started: true, Cause: "fail-fast"}},
				Tools: []report.ToolRun{
					{
						Name: "eslint", FailOn: "error", Complete: true,
						Invocations: []report.Invocation{
							{
								ID: "eslint::1#1", State: "ran", ExitCode: &one, FailureKind: "exit", Duration: 1500,
								Files: files("src/a.ts", "src/b.ts", "src/c.ts"),
								Findings: []report.Finding{
									issue("src/a.ts", 3, "error", true, "Unexpected var <x>"),
									issue("src/a.ts", 4, "warning", false, "a warning"),
									issue("src/b.ts", 1, "warning", false, "only a warning"),
								},
							},
							{ID: "eslint::2#cached", State: "cached", Success: true, Files: []report.FileResult{{Path: "src/d.ts", State: "cached", Success: true}}},
						},
					},
					{
						// A whole unit that failed on warnings: one extra case,
						// not a failure of each member.
						Name: "tsc", FailOn: "error", Complete: true,
						Invocations: []report.Invocation{{
							ID: "tsc:pkg:3#1", Dir: "pkg", State: "ran", ExitCode: &two, FailureKind: "exit", WholeUnit: true,
							Files:    files("pkg/a.ts", "pkg/b.ts"),
							Findings: []report.Finding{{Tool: "tsc", Source: "tsc", Code: "TS6133", Severity: "warning", Kind: "issue", Message: "unused", Location: report.Location{Path: "pkg/a.ts", Row: 2, EndRow: 2, Precision: "unknown"}}},
						}},
					},
					{
						Name: "shellcheck", FailOn: "error", Incomplete: []report.Reason{"no-extraction"},
						Invocations: []report.Invocation{{
							ID: "shellcheck::4#1", State: "ran", ExitCode: &one, FailureKind: "exit", Files: files("a.sh"),
							OutputTail: "a.sh: line 3: *** is not set\n",
							Findings: []report.Finding{{
								Tool: "shellcheck", Source: "shellcheck", Severity: "error", Gates: true, Kind: "synthetic",
								Message: "shellcheck exited 1 without parsable findings", Location: report.Location{Precision: "unknown"},
							}},
						}},
					},
					{
						Name: "gitleaks", FailOn: "error", Category: "security",
						Invocations: []report.Invocation{{
							ID: "gitleaks::6#1", State: "ran", ExitCode: &one, FailureKind: "exit", Files: files("secret.txt"),
							OutputTail: "never shown",
							Findings: []report.Finding{{
								Tool: "gitleaks", Source: "gitleaks", Severity: "error", Gates: true, Kind: "synthetic",
								Message: "gitleaks failed (exit 1); output withheld for a security tool", Location: report.Location{Precision: "unknown"},
							}},
						}},
					},
					{Name: "knip", FailOn: "error", Incomplete: []report.Reason{"cancelled"}, Invocations: []report.Invocation{{ID: "knip:pkg:5#1", TaskID: "knip:pkg:5", State: "cancelled", Files: []report.FileResult{{Path: "pkg/x.ts", State: "cancelled"}}}}},
				},
			},
			{Name: "fix"},
		},
	}
}

func TestRender(t *testing.T) {
	doc, raw := decode(t, sampleRun())
	if !strings.HasPrefix(raw, `<?xml version="1.0" encoding="UTF-8"?>`+"\n") {
		t.Errorf("no XML declaration:\n%s", raw)
	}
	if doc.Name != "datamitsu" {
		t.Errorf("testsuites name = %q", doc.Name)
	}

	eslint := doc.suite(t, "lint/eslint")
	if eslint.Timestamp != "2023-11-14T22:13:20" || eslint.Time != "1.500" || eslint.property("complete") != "true" || eslint.property("cached") != "1" ||
		eslint.property("run.complete") != "false" {
		t.Errorf("eslint suite = %+v", eslint)
	}
	a := eslint.testCase(t, "src/a.ts")
	if a.ClassName != "eslint" || a.Failure == nil || a.Failure.Type != "threshold" || a.Failure.Message != "1 finding at or above failOn=error" ||
		a.Failure.Text != "src/a.ts:3:3: error eslint(no-var): Unexpected var <x>" || a.SystemOut != "src/a.ts:4:3: warning eslint(no-var): a warning" {
		t.Errorf("a file with a gating finding = %+v", a)
	}
	if b := eslint.testCase(t, "src/b.ts"); b.Failure != nil || b.SystemOut != "src/b.ts:1:3: warning eslint(no-var): only a warning" {
		t.Errorf("a file with a finding below the threshold passes with it as output: %+v", b)
	}
	for _, name := range []string{"src/c.ts", "src/d.ts"} {
		if c := eslint.testCase(t, name); c.Failure != nil || c.Error != nil || c.Skipped != nil {
			t.Errorf("clean and cached files pass: %+v", c)
		}
	}
	// The failure count follows the gate, not the finding count.
	if eslint.Tests != 4 || eslint.Failures != 1 || eslint.Errors != 0 {
		t.Errorf("eslint counts = %d tests, %d failures, %d errors; want 4, 1, 0", eslint.Tests, eslint.Failures, eslint.Errors)
	}

	tsc := doc.suite(t, "lint/tsc")
	if tsc.Failures != 1 || tsc.Tests != 3 {
		t.Errorf("tsc counts = %d tests, %d failures; want the members passing and one extra case failing", tsc.Tests, tsc.Failures)
	}
	if extra := tsc.testCase(t, "pkg"); extra.Failure == nil || extra.Failure.Type != "exit" || extra.Failure.Message != "exit 2" ||
		extra.Failure.Text != "pkg/a.ts:2: warning tsc(TS6133): unused" {
		t.Errorf("the invocation's case = %+v", extra)
	}
	if member := tsc.testCase(t, "pkg/a.ts"); member.Failure != nil || member.SystemOut == "" {
		t.Errorf("a member of the failed unit = %+v, want a pass with its warning as output", member)
	}

	sh := doc.suite(t, "lint/shellcheck").testCase(t, ".")
	if sh.Error == nil || sh.Error.Type != "exit" || sh.Error.Message != "exit 1" ||
		sh.Error.Text != "shellcheck exited 1 without parsable findings\na.sh: line 3: *** is not set" {
		t.Errorf("a tool that failed without findings = %+v", sh)
	}
	if leaks := doc.suite(t, "lint/gitleaks").testCase(t, "."); leaks.Error == nil || strings.Contains(leaks.Error.Text, "never shown") {
		t.Errorf("a security tool's case = %+v, want its structured message alone", leaks)
	}

	knip := doc.suite(t, "lint/knip")
	if len(knip.Cases) != 1 || knip.Cases[0].Name != "knip:pkg:5" || knip.Cases[0].Skipped == nil ||
		knip.Cases[0].Skipped.Message != "cancelled: fail-fast" || knip.property("incomplete") != "cancelled" {
		t.Errorf("a cancelled task = %+v", knip)
	}
	if trivy := doc.suite(t, "lint/trivy"); len(trivy.Cases) != 1 || trivy.Cases[0].Skipped.Message != "skip: true: runs in CI only" {
		t.Errorf("a skipped tool = %+v", trivy)
	}
	if fix := doc.suite(t, "fix"); fix.Skipped != 1 || fix.Cases[0].Skipped.Message != "did not run" {
		t.Errorf("an operation that did not run = %+v", fix)
	}

	var failures, errors, skipped, tests int
	for _, s := range doc.Suites {
		failures, errors, skipped, tests = failures+s.Failures, errors+s.Errors, skipped+s.Skipped, tests+s.Tests
	}
	if doc.Failures != failures || doc.Errors != errors || doc.Skipped != skipped || doc.Tests != tests || failures != 2 || errors != 2 {
		t.Errorf("totals = %+v; suites add up to %d failures, %d errors, %d skipped, %d tests", doc, failures, errors, skipped, tests)
	}
}

// A check writes suites for both of its operations.
func TestCheck(t *testing.T) {
	run := sampleRun()
	run.Operations[1] = report.Operation{Name: "fix", Ran: true, Tools: []report.ToolRun{{
		Name: "prettier", Complete: false, Incomplete: []report.Reason{"no-extraction"},
		Invocations: []report.Invocation{{ID: "prettier::1#1", State: "ran", Success: true, Files: files("a.md")}},
	}}}
	doc, _ := decode(t, run)
	if s := doc.suite(t, "fix/prettier"); s.Tests != 1 || s.Failures != 0 {
		t.Errorf("fix suite = %+v", s)
	}
	doc.suite(t, "lint/eslint")
}

// A file-less finding of a whole-unit invocation is on its directory's case,
// created for it when the invocation passed.
func TestFileLessFinding(t *testing.T) {
	run := &report.Run{Operations: []report.Operation{{Name: "lint", Ran: true, Tools: []report.ToolRun{{
		Name: "tsc", FailOn: "error", Complete: true,
		Invocations: []report.Invocation{{
			ID: "tsc:web:1#1", Dir: "web", State: "ran", Success: true, WholeUnit: true, Files: files("web/a.ts"),
			Findings: []report.Finding{{Tool: "tsc", Source: "tsc", Severity: "warning", Kind: "issue", Message: "no tsconfig paths", Location: report.Location{Precision: "unknown"}}},
		}},
	}}}}}
	doc, _ := decode(t, run)
	if c := doc.suite(t, "lint/tsc").testCase(t, "web"); c.Failure != nil || c.SystemOut != "warning tsc: no tsconfig paths" {
		t.Errorf("directory case = %+v", c)
	}
}

func TestSetupFailure(t *testing.T) {
	run := &report.Run{Operations: []report.Operation{{Name: "lint", Ran: true, Tools: []report.ToolRun{{
		Name: "installer", FailOn: "error",
		Invocations: []report.Invocation{{
			ID: "installer::1#0", State: "setup-failed", FailureKind: "setup", OutputTail: "download failed\n",
			Files: []report.FileResult{{Path: "a", State: "setup-failed"}},
		}},
	}}}}}
	doc, _ := decode(t, run)
	s := doc.suite(t, "lint/installer")
	if len(s.Cases) != 1 || s.Cases[0].Error == nil || s.Cases[0].Error.Type != "setup" || s.Cases[0].Error.Text != "download failed" {
		t.Errorf("setup failure = %+v", s)
	}
}

// Two processes of a tool that failed in one directory are two cases: one
// that exited on findings below the threshold, and one that crashed, whose
// output stays with it.
func TestFailedInvocationsInOneDirectory(t *testing.T) {
	one, two := 1, 2
	run := &report.Run{Operations: []report.Operation{{Name: "lint", Ran: true, Tools: []report.ToolRun{{
		Name: "hadolint", FailOn: "error",
		Invocations: []report.Invocation{
			{
				ID: "hadolint::1#1", State: "ran", ExitCode: &one, FailureKind: "exit", Files: files("Dockerfile"),
				Findings: []report.Finding{issue("Dockerfile", 1, "warning", false, "pin it")},
			},
			{
				ID: "hadolint::1#2", State: "ran", ExitCode: &two, FailureKind: "exit", Files: files("web.Dockerfile"), OutputTail: "panic: boom\n",
				Findings: []report.Finding{{
					Tool: "hadolint", Source: "hadolint", Severity: "error", Gates: true, Kind: "synthetic",
					Message: "hadolint exited 2 without parsable findings", Location: report.Location{Precision: "unknown"},
				}},
			},
		},
	}}}}}
	doc, _ := decode(t, run)
	s := doc.suite(t, "lint/hadolint")
	first, second := s.testCase(t, ". (hadolint::1#1)"), s.testCase(t, ". (hadolint::1#2)")
	if first.Failure == nil || first.Failure.Message != "exit 1" || first.Failure.Text != "Dockerfile:1:3: warning eslint(no-var): pin it" {
		t.Errorf("the process that exited on findings = %+v", first)
	}
	if second.Error == nil || second.Error.Message != "exit 2" || second.Error.Text != "hadolint exited 2 without parsable findings\npanic: boom" {
		t.Errorf("the process that crashed = %+v", second)
	}
	if s.Failures != 1 || s.Errors != 1 {
		t.Errorf("counts = %d failures, %d errors; want one of each", s.Failures, s.Errors)
	}
}

// A process that failed the threshold on a finding another process reported
// too is not a case of its own once the duplicate is listed with the other:
// the file the finding is on fails, once.
func TestThresholdFailureAfterDedupe(t *testing.T) {
	zero := 0
	gating := issue("pkg/a.ts", 3, "error", true, "Unexpected var")
	run := &report.Run{Operations: []report.Operation{{Name: "lint", Ran: true, Tools: []report.ToolRun{{
		Name: "eslint", FailOn: "error", Complete: true,
		Invocations: []report.Invocation{
			{ID: "eslint:pkg:1#1", Dir: "pkg", State: "ran", ExitCode: &zero, FailureKind: "threshold", Files: files("pkg/a.ts"), Findings: []report.Finding{gating}},
			{ID: "eslint:pkg:2#1", Dir: "pkg", State: "ran", ExitCode: &zero, FailureKind: "threshold", Files: files("pkg/a.ts")},
		},
	}}}}}
	doc, _ := decode(t, run)
	s := doc.suite(t, "lint/eslint")
	if s.Tests != 1 || s.Failures != 1 || s.Errors != 0 {
		t.Errorf("suite = %+v, want the file failing once and nothing else", s)
	}
}

// A process the executor failed after it exited 0 — a formatter that printed
// nothing for a file that is not empty — failed on its own: an error case,
// not a pass.
func TestFailureAfterExitZero(t *testing.T) {
	zero := 0
	run := &report.Run{Operations: []report.Operation{{Name: "fix", Ran: true, Tools: []report.ToolRun{{
		Name: "fmt", FailOn: "error",
		Invocations: []report.Invocation{{
			ID: "fmt::1#1", State: "ran", ExitCode: &zero, FailureKind: "exit", Files: files("a.go"), OutputTail: "empty output\n",
		}},
	}}}}}
	doc, _ := decode(t, run)
	s := doc.suite(t, "fix/fmt")
	c := s.testCase(t, ".")
	if c.Error == nil || c.Error.Message != "exit 0" || c.Error.Text != "empty output" || s.Errors != 1 {
		t.Errorf("suite = %+v, want the invocation's error case", s)
	}
}

// A task the run never reached without a stop record — its tools could not
// be installed — is still a skipped case, not an empty suite.
func TestNotStartedWithoutStopRecord(t *testing.T) {
	run := &report.Run{Operations: []report.Operation{{Name: "lint", Ran: true, Tools: []report.ToolRun{{
		Name: "installer", FailOn: "error", Incomplete: []report.Reason{"not-started"},
		Invocations: []report.Invocation{{
			ID: "installer::1#0", TaskID: "installer::1", State: "not-started",
			Files: []report.FileResult{{Path: "a", State: "not-started"}},
		}},
	}}}}}
	doc, _ := decode(t, run)
	s := doc.suite(t, "lint/installer")
	if s.Tests != 1 || s.Skipped != 1 || s.Cases[0].Name != "installer::1" || s.Cases[0].Skipped.Message != "not started" {
		t.Errorf("suite = %+v, want one skipped case for the task", s)
	}
}

func TestEscaping(t *testing.T) {
	run := sampleRun()
	run.Operations[0].Tools[0].Invocations[0].Findings[0].Message = "quote \" amp & ctrl \x1b[31m and ]]> end\r\nnext"
	doc, raw := decode(t, run)
	got := doc.suite(t, "lint/eslint").testCase(t, "src/a.ts").Failure.Text
	if !strings.Contains(got, "quote \" amp & ctrl �[31m and ]]> end\r\nnext") {
		t.Errorf("failure text = %q", got)
	}
	if strings.Contains(raw, "\x1b") {
		t.Error("a control character XML cannot carry was written")
	}
}
