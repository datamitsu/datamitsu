package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// This file freezes the GitHub annotations of fix, lint and check: when they
// are printed, which, how they are escaped, and the stop-commands region that
// keeps tool output from printing any.

// githubEnv is a GitHub Actions push job that appends its step summary to
// summary; the harness strips the real one.
func githubEnv(summary string) []string {
	return []string{
		"GITHUB_ACTIONS=true", "GITHUB_EVENT_NAME=push", "GITHUB_SHA=0123456789abcdef0123456789abcdef01234567",
		"GITHUB_STEP_SUMMARY=" + summary,
	}
}

func summaryFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "step-summary.md")
}

// The stop-commands token is random: 32 hex characters, once per process.
var (
	stopCommandsRE = regexp.MustCompile(`::stop-commands::[0-9a-f]{32}`)
	resumeRE       = regexp.MustCompile(`(?m)^::[0-9a-f]{32}::$`)
)

func maskToken(s string) string {
	s = stopCommandsRE.ReplaceAllString(s, "::stop-commands::<TOKEN>")
	return resumeRE.ReplaceAllString(s, "::<TOKEN>::")
}

// injectedFinding is hadolint's JSON with a message whose second line is a
// workflow command.
const injectedFinding = `[{"file":"Dockerfile","line":1,"column":1,"level":"error","code":"DL3006",` +
	`"message":"Always tag the version of an image explicitly\n::error::injected"}]`

// commandPrinter fails and prints a workflow command of its own.
var commandPrinter = clitest.ShellTool("beta", settle+clitest.RecordRun+`; echo '::error title=x::y'; exit 1`, clitest.ToolOpSpec{})

// annotatedProject is a repository with a parsed tool whose finding's message
// tries to print a command, and an unparsed tool that prints one.
func annotatedProject(t *testing.T, operation string) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	hadolint := clitest.ShellTool("hadolint",
		settle+clitest.RecordRun+`; printf '%s\n' '`+injectedFinding+`'; exit 1`,
		clitest.ToolOpSpec{Operation: operation, Scope: "per-file", Globs: []string{"**/Dockerfile"}, Args: []string{"{file}"}, Parser: "hadolint"})
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, hadolint, commandPrinter))
	return e
}

func commandLines(stdout string) []string {
	var out []string
	for line := range strings.SplitSeq(stdout, "\n") {
		if strings.HasPrefix(line, "::") {
			out = append(out, line)
		}
	}
	return out
}

// TestAnnotationsGitHub: in a GitHub Actions job the results block is one
// stop-commands region, so neither a tool's own command nor a finding's
// message can print an annotation, and the run's annotations follow it.
func TestAnnotationsGitHub(t *testing.T) {
	t.Run("lint", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", githubEnv(summaryFile(t)), "lint", "--fail-fast=false")
		e.wantExit(res, 1)
		got := commandLines(maskToken(res.Stdout))
		want := []string{
			"::stop-commands::<TOKEN>",
			"::<TOKEN>::",
			"::error file=Dockerfile,line=1,col=1,title=hadolint(DL3006)::Always tag the version of an image explicitly%0A::error::injected",
			"::error title=beta::beta exited 1 without parsable findings",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("command lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if !strings.Contains(res.Stdout, "  │  ::error title=x::y\n") {
			t.Errorf("the tool's own command should print framed, inside the region:\n%s", res.Stdout)
		}
		if !strings.Contains(res.Stdout, "  │  ::error::injected [DL3006]\n") {
			t.Errorf("the second line of a parsed message should print framed:\n%s", res.Stdout)
		}
		e.golden("annotations_lint", res, maskToken)
	})

	// check opens the region with fix's results and keeps it open through
	// lint's; the annotations come before the closing line.
	t.Run("check", func(t *testing.T) {
		e := annotatedProject(t, "fix")
		res := e.run("", githubEnv(summaryFile(t)), "check", "--fail-fast=false")
		e.wantExit(res, 1)
		out := maskToken(res.Stdout)
		if strings.Count(out, "::stop-commands::<TOKEN>") != 1 || strings.Count(out, "::<TOKEN>::") != 1 {
			t.Errorf("one region per run, opened once and closed once:\n%s", out)
		}
		if closing := strings.Index(out, "┗━ check"); closing < strings.LastIndex(out, "::error ") {
			t.Errorf("the annotations should precede check's closing line:\n%s", out)
		}
		e.golden("annotations_check", res, maskToken)
	})

	t.Run("report_records_the_annotations", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", githubEnv(summaryFile(t)), "lint", "--report", "json=run.json")
		e.wantExit(res, 1)
		_, doc := e.report("run.json")
		exports, _ := doc["exports"].([]any)
		formats := make([]string, 0, len(exports))
		for _, x := range exports {
			m, _ := x.(map[string]any)
			formats = append(formats, m["format"].(string)+":"+m["status"].(string))
		}
		if strings.Join(formats, " ") != "json:written github-annotations:written" {
			t.Errorf("exports = %v, want the report and the annotations, both written", formats)
		}
		ci, _ := doc["ci"].(map[string]any)
		if ci["vendor"] != "github" || ci["sha"] != "0123456789abcdef0123456789abcdef01234567" {
			t.Errorf("ci = %v, want the GitHub job's vendor and commit", ci)
		}
	})
}

// TestAnnotationsWhenNot: auto prints nothing outside GitHub Actions, beside a
// JSON-L stream or a document on stdout, and nothing when no task ran; an
// explicit github prints beside a JSON-L stream and is refused beside a
// document.
func TestAnnotationsWhenNot(t *testing.T) {
	t.Run("outside_github", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", nil, "lint")
		e.wantExit(res, 1)
		if lines := commandLines(res.Stdout); len(lines) != 0 {
			t.Errorf("no workflow command outside GitHub Actions, got %q", lines)
		}
	})

	// The variable turns them off; the flag wins over it.
	t.Run("variable_off_flag_wins", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		env := append([]string{"DATAMITSU_ANNOTATIONS=off"}, githubEnv(summaryFile(t))...)
		res := e.run("", env, "lint")
		e.wantExit(res, 1)
		if lines := commandLines(res.Stdout); len(lines) != 0 {
			t.Errorf("DATAMITSU_ANNOTATIONS=off printed %q", lines)
		}
		res = e.run("", []string{"DATAMITSU_ANNOTATIONS=off"}, "lint", "--annotations", "github")
		e.wantExit(res, 1)
		if lines := commandLines(res.Stdout); len(lines) == 0 {
			t.Error("--annotations github should win over DATAMITSU_ANNOTATIONS=off, outside GitHub too")
		}
	})

	t.Run("jsonl", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", githubEnv(summaryFile(t)), jsonl("lint")...)
		e.wantExit(res, 1)
		if res.Stdout != "" {
			t.Errorf("auto under --log-format jsonl prints nothing, got:\n%s", res.Stdout)
		}
		events := clitest.MustParseJSONL(t, res.Stderr)
		wantReportEvent(t, events, "github-annotations", "-", "omitted", "the run writes a JSON-L event stream")
		if hello := events[0]; hello.Type != "hello" || hello.Fields["annotations"] != "off" {
			t.Errorf("the stream opens with %v, want a hello saying stdout carries no annotations", hello.Fields)
		}
	})

	t.Run("jsonl_explicit", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", githubEnv(summaryFile(t)), jsonl("lint", "--annotations", "github", keepGoing)...)
		e.wantExit(res, 1)
		want := []string{
			"::error file=Dockerfile,line=1,col=1,title=hadolint(DL3006)::Always tag the version of an image explicitly%0A::error::injected",
			"::error title=beta::beta exited 1 without parsable findings",
		}
		if got := strings.TrimSuffix(res.Stdout, "\n"); got != strings.Join(want, "\n") {
			t.Errorf("stdout =\n%s\nwant the annotations alone:\n%s", got, strings.Join(want, "\n"))
		}
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if hello := events[0]; hello.Type != "hello" || hello.Fields["annotations"] != "github" {
			t.Errorf("the stream opens with %v, want a hello saying stdout carries annotations", hello.Fields)
		}
	})

	t.Run("report_on_stdout", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", githubEnv(summaryFile(t)), "lint", "--report", "json=-")
		e.wantExit(res, 1)
		var doc map[string]any
		if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
			t.Fatalf("stdout should hold the document alone: %v\n%s", err, res.Stdout)
		}
	})

	t.Run("empty_plan", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
			clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{Globs: []string{"**/*.none"}}))
		res := e.run("", githubEnv(summaryFile(t)), "lint")
		e.wantExit(res, 0)
		if strings.Contains(res.Stdout, "::") {
			t.Errorf("a run in which no task ran prints no workflow command:\n%s", res.Stdout)
		}
	})
}

func TestAnnotationsUsage(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		args []string
		// jsonl marks a run whose report on stdout made stderr a JSON-L stream.
		jsonl bool
	}{
		{name: "github_with_report_on_stdout", jsonl: true, args: []string{"lint", "--annotations", "github", "--report", "json=-"}},
		{name: "github_env_with_report_on_stdout", jsonl: true, env: []string{"DATAMITSU_ANNOTATIONS=github"}, args: []string{"lint", "--report", "json=-"}},
		{name: "github_with_explain_json", args: []string{"lint", "--annotations", "github", "--explain=json"}},
		{name: "invalid_flag", args: []string{"lint", "--annotations", "azure"}},
		{name: "invalid_env", env: []string{"DATAMITSU_ANNOTATIONS=on"}, args: []string{"lint", "--annotations", "off"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
				clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
			res := e.run("", tc.env, tc.args...)
			e.wantExit(res, 2)
			e.wantMarker("alpha", "")
			if tc.jsonl {
				e.goldenJSONL("annotations_usage_"+tc.name, res)
				return
			}
			e.golden("annotations_usage_"+tc.name, res)
		})
	}
}
