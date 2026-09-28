package cli_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes --output agent: one line per finding the terminal would
// show, one per task the run did not finish, one summary line per operation,
// and nothing else — no banner, frame, colour or progress.

const agentOutput = "--output=agent"

// TestAgentOutput is S2 and S5 with --fail-fast=false, read by an agent.
func TestAgentOutput(t *testing.T) {
	t.Run("s2_priorities", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
			clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{Priority: 10}),
			clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{Priority: 20}))
		res := e.run("", nil, "lint", keepGoing, agentOutput)
		e.wantExit(res, 1)
		want := "alpha exited 1 without parsable findings\n" +
			"  │  alpha: failed\n" +
			"lint: 2 tools · 2 runs · 1 failed · 0 errors 0 warnings · 0 hidden\n"
		if res.Stdout != want {
			t.Errorf("stdout =\n%s\nwant\n%s", res.Stdout, want)
		}
		e.golden("agent_s2_keep_going", res)
	})

	t.Run("s5_per_file_loop", func(t *testing.T) {
		e := newExecProject(t, perFileLoopFiles, fixtureSpec, perFileLoopTool)
		res := e.run("", nil, "lint", keepGoing, agentOutput)
		e.wantExit(res, 1)
		e.golden("agent_s5_keep_going", res)
	})

	// Fail-fast is not implied: the tasks the run stopped are records too.
	t.Run("s2_fail_fast", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
			clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{Priority: 10}),
			clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{Priority: 20}))
		res := e.run("", nil, "lint", agentOutput)
		e.wantExit(res, 1)
		if !strings.Contains(res.Stdout, "beta: not started (fail-fast)\n") {
			t.Errorf("the task fail-fast stopped should be a record:\n%s", res.Stdout)
		}
	})

	// In a GitHub Actions job the records are tool text like any other: they
	// print inside the stop-commands region, the annotations after it.
	t.Run("github", func(t *testing.T) {
		e := annotatedProject(t, "lint")
		res := e.run("", githubEnv(summaryFile(t)), "lint", keepGoing, agentOutput)
		e.wantExit(res, 1)
		out := maskToken(res.Stdout)
		region := strings.Index(out, "::stop-commands::<TOKEN>\n")
		closing := strings.Index(out, "::<TOKEN>::\n")
		record := strings.Index(out, "Dockerfile:1:1: error hadolint(DL3006)")
		if region != 0 || record < region || closing < record {
			t.Errorf("the records should print inside the region:\n%s", out)
		}
		e.golden("agent_github", res, maskToken)
	})

	// check keeps its closing wall-clock line, plain.
	t.Run("check", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
			clitest.ShellTool("gamma", passScript, clitest.ToolOpSpec{Operation: "fix"}),
			clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
		res := e.run("", nil, "check", agentOutput)
		e.wantExit(res, 0)
		lines := strings.Split(strings.TrimSuffix(res.Stdout, "\n"), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "fix: 1 tools · 1 runs · 0 failed") ||
			!strings.HasPrefix(lines[1], "lint: 1 tools · 1 runs · 0 failed") ||
			!regexp.MustCompile(`^check · done in \S+ · fix \S+ · lint \S+ · setup \S+$`).MatchString(lines[2]) {
			t.Errorf("stdout =\n%s\nwant a summary per operation and check's closing line", res.Stdout)
		}
	})
}

// findingRE reads a finding the terminal prints in a frame.
var findingRE = regexp.MustCompile(`(?m)^  │  (\S+):(\d+):(\d+) (\w+) (.*) \[(\S+)\]$`)

// agentFindingRE reads a finding record.
var agentFindingRE = regexp.MustCompile(`(?m)^(\S+):(\d+):(\d+): (\w+) \w+\((\S+)\): (.*)$`)

func humanFindings(stdout string) []string {
	matches := findingRE.FindAllStringSubmatch(stdout, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.Join([]string{m[1], m[2], m[3], m[4], m[6], m[5]}, " "))
	}
	slices.Sort(out)
	return out
}

func agentFindings(stdout string) []string {
	matches := agentFindingRE.FindAllStringSubmatch(stdout, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.Join([]string{m[1], m[2], m[3], m[4], m[5], m[6]}, " "))
	}
	slices.Sort(out)
	return out
}

// TestAgentOutputShowsWhatTheTerminalShows: both outputs print the same
// findings, chosen by the same rule — every warning of a tool that failed on
// warnings alone, and only what reaches failOn of a tool that failed on an
// error.
func TestAgentOutputShowsWhatTheTerminalShows(t *testing.T) {
	warnings := `[{"file":"Dockerfile","line":1,"column":1,"level":"warning","code":"DL3006","message":"Always tag"},` +
		`{"file":"Dockerfile","line":2,"column":1,"level":"warning","code":"DL3008","message":"Pin versions"}]`
	gating := `[{"file":"Dockerfile","line":1,"column":1,"level":"error","code":"DL3000","message":"Use absolute WORKDIR"},` +
		`{"file":"Dockerfile","line":2,"column":1,"level":"warning","code":"DL3008","message":"Pin versions"}]`
	for _, tc := range []struct {
		name, output string
		want         int
	}{
		{name: "warnings_only_failure", output: warnings, want: 2},
		{name: "gating", output: gating, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newParsedProject(t, tc.output, 1)
			human := e.run("", nil, "lint")
			agent := e.run("", nil, "lint", agentOutput)
			e.wantExit(human, 1)
			e.wantExit(agent, 1)
			h, a := humanFindings(human.Stdout), agentFindings(agent.Stdout)
			if len(h) != tc.want || !slices.Equal(h, a) {
				t.Errorf("human shows %q, agent %q; want the same %d", h, a, tc.want)
			}
		})
	}
}

// A message that spans lines is one record, its line breaks written as \n.
func TestAgentOutputMessageIsOneRecord(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, clitest.ShellTool("hadolint",
		settle+clitest.RecordRun+`; printf '%s\n' '`+injectedFinding+`'; exit 1`,
		clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/Dockerfile"}, Args: []string{"{file}"}, Parser: "hadolint"})))
	res := e.run("", nil, "lint", agentOutput)
	e.wantExit(res, 1)
	const want = "Dockerfile:1:1: error hadolint(DL3006): Always tag the version of an image explicitly\\n::error::injected\n"
	if !strings.HasPrefix(res.Stdout, want) {
		t.Errorf("stdout =\n%s\nwant it to start with the one record\n%s", res.Stdout, want)
	}
}

func TestAgentOutputUsage(t *testing.T) {
	cases := []struct {
		name  string
		env   []string
		args  []string
		jsonl bool
	}{
		{name: "invalid_flag", args: []string{"lint", "--output", "json"}},
		{name: "invalid_env", env: []string{"DATAMITSU_OUTPUT=robot"}, args: []string{"lint", "--output", "human"}},
		{name: "with_report_on_stdout", jsonl: true, args: []string{"lint", agentOutput, "--report", "json=-"}},
		{name: "with_jsonl", jsonl: true, args: []string{"--log-format", "jsonl", "lint", agentOutput}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
				clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
			res := e.run("", tc.env, tc.args...)
			e.wantExit(res, 2)
			e.wantMarker("alpha", "")
			if tc.jsonl {
				e.goldenJSONL("agent_usage_"+tc.name, res)
				return
			}
			e.golden("agent_usage_"+tc.name, res)
		})
	}
}
