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

// This file freezes --report: the document a run writes, when it is written,
// and the exit code of a report that could not be.

// reportDurationRE masks what a document measured: the durations are real.
var reportDurationRE = regexp.MustCompile(`"durationMs": \d+`)

// reportProject is a repository with a passing tool and a parsed tool that
// exits 0 on a warning, so a document holds a clean invocation and a finding.
func reportProject(t *testing.T, extra ...string) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	tools := append([]string{
		clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}),
		parsedTool(hadolintFinding, 0),
	}, extra...)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tools...))
	return e
}

// report reads a document the run wrote and returns it normalized, with its
// decoded form.
func (e *execProject) report(rel string) (string, map[string]any) {
	e.t.Helper()
	raw := e.read(rel)
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		e.t.Fatalf("%s is not JSON: %v\n%s", rel, err, raw)
	}
	return e.normalize(reportDurationRE.ReplaceAllString(raw, `"durationMs": <DUR>`)), doc
}

func (e *execProject) goldenReport(name, doc string) {
	e.t.Helper()
	clitest.AssertGolden(e.t, "report_"+name, doc)
}

func operationsOf(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	raw, _ := doc["operations"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, op := range raw {
		m, _ := op.(map[string]any)
		out = append(out, m)
	}
	return out
}

// TestReportJSON: a run writes its own document once its last operation has
// ended, stamped with SOURCE_DATE_EPOCH, whether or not a tool failed.
func TestReportJSON(t *testing.T) {
	t.Run("lint", func(t *testing.T) {
		e := reportProject(t)
		res := e.run("", nil, "lint", "--report", "json=out/run.json")
		e.wantExit(res, 0)
		doc, decoded := e.report("out/run.json")
		const stamp = "2023-11-14T22:13:20Z"
		if decoded["startedAt"] != stamp || decoded["endedAt"] != stamp {
			t.Errorf("startedAt, endedAt = %v, %v; want both %s from SOURCE_DATE_EPOCH", decoded["startedAt"], decoded["endedAt"], stamp)
		}
		for _, leak := range []string{`"command"`, `"args"`, "MARKERS"} {
			if strings.Contains(doc, leak) {
				t.Errorf("the document carries %s; argv and the environment never enter a report:\n%s", leak, doc)
			}
		}
		e.goldenReport("lint", doc)
		e.golden("report_lint", res)
	})

	// check writes one document for both of its operations.
	t.Run("check", func(t *testing.T) {
		e := reportProject(t, clitest.ShellTool("gamma", passScript, clitest.ToolOpSpec{Operation: "fix"}))
		res := e.run("", nil, "check", "--report", "json=run.json")
		e.wantExit(res, 0)
		doc, decoded := e.report("run.json")
		ops := operationsOf(t, decoded)
		if len(ops) != 2 || ops[0]["name"] != "fix" || ops[1]["name"] != "lint" {
			t.Errorf("operations = %v, want fix then lint", ops)
		}
		e.goldenReport("check", doc)
	})

	// The run that fails is the one a pipeline uploads.
	t.Run("tool_failed", func(t *testing.T) {
		e := reportProject(t, clitest.ShellTool("beta", failScript, clitest.ToolOpSpec{}))
		res := e.run("", nil, "lint", "--report", "json=run.json")
		e.wantExit(res, 1)
		doc, decoded := e.report("run.json")
		if ops := operationsOf(t, decoded); len(ops) != 1 || ops[0]["success"] != false {
			t.Errorf("operations = %v, want one failed lint", ops)
		}
		e.goldenReport("tool_failed", doc)
	})

	t.Run("stdout", func(t *testing.T) {
		e := reportProject(t)
		res := e.run("", nil, "lint", "--report", "json=-")
		e.wantExit(res, 0)
		var decoded map[string]any
		if err := json.Unmarshal([]byte(res.Stdout), &decoded); err != nil {
			t.Fatalf("stdout is not the document alone: %v\n%s", err, res.Stdout)
		}
		if decoded["schema"] != "datamitsu.report/1" {
			t.Errorf("schema = %v, want datamitsu.report/1", decoded["schema"])
		}
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantReportEvent(t, events, "json", "-", "written", "")
	})

	t.Run("event", func(t *testing.T) {
		e := reportProject(t)
		res := e.run("", nil, jsonl("lint", "--report", "json=run.json")...)
		e.wantExit(res, 0)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantReportEvent(t, events, "json", "run.json", "written", "")
	})
}

// TestReportNotWritten: a report that cannot be written exits 5 when nothing
// else failed and leaves the exit code of a failed tool alone, printing why
// either way.
func TestReportNotWritten(t *testing.T) {
	t.Run("exit_5", func(t *testing.T) {
		e := reportProject(t)
		e.p.WriteFile("blocker", "a file, not a directory\n")
		res := e.run("", nil, "lint", "--report", "json=blocker/run.json")
		e.wantExit(res, 5)
		if !strings.Contains(res.Stderr, "error: report json: blocker/run.json: not a directory") {
			t.Errorf("stderr should say which report was not written and why:\n%s", res.Stderr)
		}
		e.golden("report_not_written", res)
	})

	t.Run("tool_failure_wins", func(t *testing.T) {
		e := reportProject(t, clitest.ShellTool("beta", failScript, clitest.ToolOpSpec{}))
		e.p.WriteFile("blocker", "a file, not a directory\n")
		res := e.run("", []string{"DATAMITSU_REPORT=json=ok.json"}, "lint", "--report", "json=blocker/run.json")
		e.wantExit(res, 1)
		if !strings.Contains(res.Stderr, "error: report json: blocker/run.json: not a directory") {
			t.Errorf("the failed write should still be printed:\n%s", res.Stderr)
		}
		if _, err := os.Stat(filepath.Join(e.p.Dir, "ok.json")); err == nil {
			t.Error("the flag names json, so DATAMITSU_REPORT's json entry should have been dropped")
		}
	})

	t.Run("event", func(t *testing.T) {
		e := reportProject(t)
		e.p.WriteFile("blocker", "a file, not a directory\n")
		res := e.run("", nil, jsonl("lint", "--report", "json=blocker/run.json")...)
		e.wantExit(res, 5)
		events := clitest.MustParseJSONL(t, res.Stderr)
		wantReportEvent(t, events, "json", "blocker/run.json", "failed", "not a directory")
	})
}

// TestReportUsage: a report asked for in a way that cannot be read is refused
// before anything runs, from the flag and the variable alike.
func TestReportUsage(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		args []string
	}{
		{name: "env_unknown_format", env: []string{"DATAMITSU_REPORT=yaml=out.yaml"}, args: []string{"lint"}},
		{name: "env_no_path", env: []string{"DATAMITSU_REPORT=json"}, args: []string{"lint"}},
		{name: "flag_unknown_option", args: []string{"lint", "--report", "json=out.json?category=x"}},
		{name: "flag_twice", args: []string{"lint", "--report", "json=a.json", "--report", "json=b.json"}},
		{name: "with_explain", args: []string{"lint", "--explain", "--report", "json=out.json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := reportProject(t)
			res := e.run("", tc.env, tc.args...)
			e.wantExit(res, 2)
			e.wantMarker("alpha", "")
			e.golden("report_usage_"+tc.name, res)
		})
	}
}

// TestReportNarrowedRun: a report that lists findings is refused, before
// anything runs, for a run narrowed at plan time; --allow-partial writes it
// with every reason it is incomplete, and never makes it complete.
func TestReportNarrowedRun(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		args []string
	}{
		{name: "paths", args: []string{"lint", "Dockerfile"}},
		{name: "subtree", dir: "sub", args: []string{"lint"}},
		{name: "tools", args: []string{"lint", "--tools", "hadolint"}},
		{name: "file_scoped", args: []string{"lint", "--file-scoped"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := reportProject(t)
			e.p.WriteFile("sub/keep.txt", "")
			refused := e.run(tc.dir, nil, append(tc.args, "--report", "json=run.json")...)
			e.wantExit(refused, 2)
			e.wantMarker("alpha", "")
			e.wantMarker("hadolint", "")
			e.golden("report_refused_"+tc.name, refused)

			events := e.run(tc.dir, nil, jsonl(append(tc.args, "--report", "json=run.json")...)...)
			e.wantExit(events, 2)
			wantReportEvent(t, clitest.MustParseJSONL(t, events.Stderr), "json", "run.json", "refused", "the run is narrowed")

			allowed := e.run(tc.dir, nil, append(tc.args, "--report", "json=run.json", "--allow-partial")...)
			e.wantExit(allowed, 0)
			doc, decoded := e.report(filepath.Join(tc.dir, "run.json"))
			if decoded["complete"] != false {
				t.Errorf("--allow-partial made the run complete:\n%s", doc)
			}
			e.goldenReport("allow_partial_"+tc.name, doc)
		})
	}

	t.Run("env", func(t *testing.T) {
		e := reportProject(t)
		res := e.run("", []string{"DATAMITSU_ALLOW_PARTIAL=1"}, "lint", "Dockerfile", "--report", "json=run.json")
		e.wantExit(res, 0)
		res = e.run("", []string{"DATAMITSU_ALLOW_PARTIAL=1"}, "lint", "Dockerfile", "--report", "json=run.json", "--allow-partial=false")
		e.wantExit(res, 2)
		res = e.run("", []string{"DATAMITSU_ALLOW_PARTIAL=yes"}, "lint", "--report", "json=run.json")
		e.wantExit(res, 2)
		e.golden("report_allow_partial_invalid_env", res)
	})
}

// TestReportKeepsGoing: a report turns fail-fast off, so a failing tool does
// not stop the ones after it (S2's twin); fail-fast asked for explicitly is a
// usage error.
func TestReportKeepsGoing(t *testing.T) {
	files := map[string]string{"fixture.marker": ""}
	tools := []string{
		clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{Priority: 10}),
		clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{Priority: 20}),
	}

	t.Run("fail_fast_off", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "lint", "--report", "json=run.json")
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "beta \n")
		doc, decoded := e.report("run.json")
		if decoded["failFast"] != false {
			t.Errorf("failFast = %v, want false: a report turns it off", decoded["failFast"])
		}
		e.goldenReport("keep_going", doc)
	})

	t.Run("flag_wins_over_env", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", []string{"DATAMITSU_FAIL_FAST=true"}, "lint", "--fail-fast=false", "--report", "json=run.json")
		e.wantExit(res, 1)
		e.wantMarker("beta", "beta \n")
	})

	for _, tc := range []struct {
		name string
		env  []string
		args []string
	}{
		{name: "flag", args: []string{"lint", "--fail-fast=true", "--report", "json=run.json"}},
		{name: "env", env: []string{"DATAMITSU_FAIL_FAST=true", "DATAMITSU_REPORT=json=run.json"}, args: []string{"lint"}},
	} {
		t.Run("explicit_"+tc.name, func(t *testing.T) {
			e := newExecProject(t, files, fixtureSpec, tools...)
			res := e.run("", tc.env, tc.args...)
			e.wantExit(res, 2)
			e.wantMarker("alpha", "")
			e.golden("report_fail_fast_"+tc.name, res)
		})
	}
}

// TestReportMasksSecrets: a value of a variable whose name says it holds a
// secret is masked wherever a report would carry it — a finding's message, a
// failed tool's output — and a tool that fails without a finding is a
// synthetic finding whose message carries no output.
func TestReportMasksSecrets(t *testing.T) {
	const secret = "abcdefgh12"
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	leakyFinding := `[{"file":"Dockerfile","line":1,"column":1,"level":"warning","code":"DL3006",` +
		`"message":"token '"$DATAMITSU_TEST_TOKEN"' in the image"}]`
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec,
		clitest.ShellTool("leaky", settle+clitest.RecordRun+`; echo "using $DATAMITSU_TEST_TOKEN"; exit 3`, clitest.ToolOpSpec{}),
		parsedTool(leakyFinding, 0),
	))
	res := e.run("", []string{"DATAMITSU_TEST_TOKEN=" + secret}, "lint", "--report", "json=run.json")
	e.wantExit(res, 1)
	doc, _ := e.report("run.json")
	if strings.Contains(doc, secret) {
		t.Errorf("the report holds the secret:\n%s", doc)
	}
	for _, want := range []string{
		`"message": "token *** in the image"`, `"outputTail": "using ***\n"`,
		`"message": "leaky exited 3 without parsable findings"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the report lacks %s:\n%s", want, doc)
		}
	}
	e.goldenReport("masked", doc)
}

// TestReportSecurityTool: the output of a security tool never enters a report,
// and its synthetic finding says so.
func TestReportSecurityTool(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, clitest.ShellTool("gitleaks",
		settle+clitest.RecordRun+`; echo "leak: found-secret-value"; exit 1`,
		clitest.ToolOpSpec{Parser: "gitleaks"})))
	res := e.run("", nil, "lint", "--report", "json=run.json")
	e.wantExit(res, 1)
	doc, _ := e.report("run.json")
	if strings.Contains(doc, "found-secret-value") || strings.Contains(doc, "outputTail") {
		t.Errorf("the report carries a security tool's output:\n%s", doc)
	}
	if !strings.Contains(doc, `"message": "gitleaks failed (exit 1); output withheld for a security tool"`) ||
		!strings.Contains(doc, `"category": "security"`) {
		t.Errorf("the report lacks the security tool's synthetic finding:\n%s", doc)
	}
}

// TestReportRender: `report render` converts a run's own JSON offline through
// the same renderers and the same completeness rule; its json target
// reproduces the document byte for byte, without the checkout.
func TestReportRender(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		e := reportProject(t)
		e.wantExit(e.run("", nil, "lint", "--report", "json=run.json"), 0)
		original := e.read("run.json")
		if err := os.Remove(filepath.Join(e.p.Dir, "Dockerfile")); err != nil {
			t.Fatal(err)
		}

		res := e.run("", nil, "report", "render", "--input", "run.json", "--format", "json", "--output", "out/again.json")
		e.wantExit(res, 0)
		if again := e.read("out/again.json"); again != original {
			t.Errorf("render changed the document:\n--- run\n%s\n--- render\n%s", original, again)
		}
		stdout := e.run("", nil, "report", "render", "--input", "run.json", "--format", "json")
		e.wantExit(stdout, 0)
		if stdout.Stdout != original || stdout.Stderr != "" {
			t.Errorf("render to stdout = %q (stderr %q), want the document alone", stdout.Stdout, stdout.Stderr)
		}
	})

	t.Run("narrowed", func(t *testing.T) {
		e := reportProject(t)
		e.wantExit(e.run("", nil, "lint", "Dockerfile", "--allow-partial", "--report", "json=run.json"), 0)
		refused := e.run("", nil, "report", "render", "--input", "run.json", "--format", "json")
		e.wantExit(refused, 2)
		e.golden("report_render_narrowed", refused)
		allowed := e.run("", nil, "report", "render", "--input", "run.json", "--format", "json", "--allow-partial")
		e.wantExit(allowed, 0)
		if allowed.Stdout != e.read("run.json") {
			t.Error("--allow-partial rendered another document")
		}
	})

	t.Run("errors", func(t *testing.T) {
		e := reportProject(t)
		e.p.WriteFile("future.json", `{"schema": "datamitsu.report/2", "complete": true}`)
		e.p.WriteFile("blocker", "a file, not a directory\n")
		e.p.WriteFile("run.json", `{"schema": "datamitsu.report/1", "selection": {"mode": "all"}}`)
		for _, tc := range []struct {
			name string
			args []string
			exit int
		}{
			{name: "future_schema", args: []string{"--input", "future.json", "--format", "json"}, exit: 1},
			{name: "missing_input", args: []string{"--input", "none.json", "--format", "json"}, exit: 1},
			{name: "unknown_format", args: []string{"--input", "run.json", "--format", "yaml"}, exit: 2},
			{name: "unknown_option", args: []string{"--input", "run.json", "--format", "json?category=x"}, exit: 2},
			{name: "no_input_flag", args: []string{"--format", "json"}, exit: 2},
			{name: "unwritable", args: []string{"--input", "run.json", "--format", "json", "--output", "blocker/x.json"}, exit: 5},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res := e.run("", nil, append([]string{"report", "render"}, tc.args...)...)
				e.wantExit(res, tc.exit)
				e.golden("report_render_"+tc.name, res)
			})
		}
	})

	t.Run("help", func(t *testing.T) {
		norm := clitest.NewNormalizer()
		for _, args := range [][]string{{"report", "--help"}, {"report", "render", "--help"}} {
			res := clitest.Run(t, clitest.RunOptions{}, args...)
			if res.ExitCode != 0 {
				t.Fatalf("%v exit = %d\n%s", args, res.ExitCode, res.Stderr)
			}
			clitest.AssertGolden(t, strings.Join(args[:len(args)-1], "_")+"_help", norm.Apply(res.Stdout))
		}
	})
}

func wantReportEvent(t *testing.T, events []clitest.Event, format, path, status, msg string) {
	t.Helper()
	got := eventsOf(events, func(e clitest.Event) bool { return e.Type == "report" })
	if len(got) != 1 {
		t.Fatalf("report events = %+v, want one", got)
	}
	e := got[0]
	if e.Fields["format"] != format || e.Fields["path"] != path || e.Status != status || !strings.Contains(e.Msg, msg) {
		t.Errorf("report event = %v, want format %s, path %s, status %s, msg containing %q", e.Fields, format, path, status, msg)
	}
}
