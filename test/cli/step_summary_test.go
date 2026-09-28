package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// TestStepSummary: in a GitHub Actions job the run appends its Markdown to the
// step summary, within what the page has left, and a summary it cannot write
// is one warning that changes no exit code.
func TestStepSummary(t *testing.T) {
	t.Run("written", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		summary := summaryFile(t)
		res := e.run("", githubEnv(summary), "lint", keepGoing)
		e.wantExit(res, 1)
		if strings.Contains(res.Stderr, "step summary") {
			t.Errorf("a written summary warns nothing:\n%s", res.Stderr)
		}
		clitest.AssertGolden(t, "step_summary_lint", readFile(t, summary))
	})

	t.Run("appends_within_the_limit", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		summary := summaryFile(t)
		const limit = 1 << 20
		prefix := strings.Repeat("earlier step output\n", (limit-700)/20)
		if err := os.WriteFile(summary, []byte(prefix), 0o644); err != nil {
			t.Fatal(err)
		}
		res := e.run("", githubEnv(summary), "lint", keepGoing)
		e.wantExit(res, 1)
		doc := readFile(t, summary)
		if len(doc) > limit || !strings.HasPrefix(doc, prefix) {
			t.Fatalf("the summary is %d bytes, over GitHub's %d, or lost what was there", len(doc), limit)
		}
		if !strings.Contains(doc[len(prefix):], "cut: the page ran out of room") {
			t.Errorf("a summary that ran out of room says so:\n%s", doc[len(prefix):])
		}
	})

	t.Run("unset", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", []string{"GITHUB_ACTIONS=true", "GITHUB_EVENT_NAME=push"}, "lint", keepGoing)
		e.wantExit(res, 1)
		if n := strings.Count(res.Stderr, "the step summary was not written: GITHUB_STEP_SUMMARY is not set"); n != 1 {
			t.Errorf("want one warning, got %d:\n%s", n, res.Stderr)
		}
	})

	t.Run("unwritable", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
			clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
		res := e.run("", githubEnv(t.TempDir()), "lint")
		e.wantExit(res, 0)
		if n := strings.Count(res.Stderr, "the step summary was not written:"); n != 1 {
			t.Errorf("want one warning for a summary that is a directory, got %d:\n%s", n, res.Stderr)
		}
	})

	t.Run("jsonl", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		summary := summaryFile(t)
		res := e.run("", githubEnv(summary), jsonl("lint", keepGoing)...)
		e.wantExit(res, 1)
		if doc := readFile(t, summary); !strings.Contains(doc, "`hadolint(DL3006)`") {
			t.Errorf("the summary is written under --log-format jsonl too:\n%s", doc)
		}
	})

	t.Run("off", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		summary := summaryFile(t)
		res := e.run("", githubEnv(summary), "lint", "--annotations", "off")
		e.wantExit(res, 1)
		if _, err := os.Stat(summary); err == nil {
			t.Error("--annotations off writes no step summary")
		}
	})
}

// TestReportMarkdown: --report markdown writes the document a step summary
// holds, and report render gives it again from the run's own JSON.
func TestReportMarkdown(t *testing.T) {
	e := reportProject(t, clitest.ShellTool("beta", failScript, clitest.ToolOpSpec{}))
	res := e.run("", nil, "lint", "--report", "markdown=out/run.md", "--report", "json=out/run.json")
	e.wantExit(res, 1)
	doc := e.read("out/run.md")
	clitest.AssertGolden(t, "report_markdown", doc)

	again := e.run("", nil, "report", "render", "--input", "out/run.json", "--format", "markdown")
	e.wantExit(again, 0)
	if again.Stdout != doc {
		t.Errorf("report render gives another document:\n%s\nwant\n%s", again.Stdout, doc)
	}
}
