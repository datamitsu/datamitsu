package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes what --log-format jsonl says about findings: the hello
// that opens the stream, one diagnostic event per finding once its tool has
// finished, and the per-level counters of every finished task.

// hadolintErrorAndWarning is an error and a warning on the first line.
const hadolintErrorAndWarning = `[{"file":"Dockerfile","line":1,"column":1,"level":"error","code":"DL3000",` +
	`"message":"Use absolute WORKDIR"},` +
	`{"file":"Dockerfile","line":1,"column":1,"level":"warning","code":"DL3006",` +
	`"message":"Always tag the version of an image explicitly"}]`

var fingerprintRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func eventsProject(t *testing.T) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec,
		parsedTool(hadolintErrorAndWarning, 0),
		clitest.ShellTool("broken", failScript, clitest.ToolOpSpec{}),
	))
	return e
}

func diagnosticsOf(events []clitest.Event) []clitest.Event {
	return eventsOf(events, func(e clitest.Event) bool { return e.Type == "diagnostic" })
}

func TestEventsDiagnostics(t *testing.T) {
	t.Run("reported", func(t *testing.T) {
		e := eventsProject(t)
		res := e.run("", nil, jsonl("lint", "--report", "json=run.json")...)
		e.wantExit(res, 1)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)

		hello := events[0]
		if hello.Type != "hello" || hello.OpID != "stream" || hello.Fields["schema"] != "datamitsu.report/1" ||
			!strings.Contains(hello.Fields["events"].(string), "diagnostic") {
			t.Errorf("the stream opens with %v, want a hello naming the schema and the diagnostic events", hello.Fields)
		}

		diags := diagnosticsOf(events)
		if len(diags) != 1 {
			t.Fatalf("diagnostic events = %v, want the error alone: the warning is below failOn", diags)
		}
		d := diags[0].Fields
		if d["tool"] != "hadolint" || d["file"] != "Dockerfile" || d["code"] != "DL3000" || d["severity"] != "error" ||
			d["reported"] != true || d["gates"] != true || fmt.Sprint(d["row"]) != "1" || d["provenance"] != "parser" ||
			!fingerprintRE.MatchString(d["fingerprint"].(string)) {
			t.Errorf("diagnostic event = %v", d)
		}
		run := toolRuns(events, "hadolint")
		if diags[0].OpID != run[0].OpID {
			t.Errorf("diagnostic op_id = %s, want its task's %s", diags[0].OpID, run[0].OpID)
		}

		_, doc := e.report("run.json")
		if fp := findingFingerprints(doc)["DL3000"]; fp != d["fingerprint"] {
			t.Errorf("the event's fingerprint %v differs from the report's %s", d["fingerprint"], fp)
		}
	})

	for _, tc := range []struct {
		name string
		env  []string
		args []string
	}{
		{name: "flag", args: []string{"lint", keepGoing, "--events", "diagnostics=all"}},
		{name: "env", env: []string{"DATAMITSU_EVENTS=diagnostics=all"}, args: []string{"lint", keepGoing}},
		{name: "flag_wins", env: []string{"DATAMITSU_EVENTS=diagnostics=reported"}, args: []string{"lint", keepGoing, "--events=diagnostics=all"}},
	} {
		t.Run("all_"+tc.name, func(t *testing.T) {
			e := eventsProject(t)
			res := e.run("", tc.env, jsonl(tc.args...)...)
			e.wantExit(res, 1)
			events := clitest.MustParseJSONL(t, res.Stderr)
			clitest.AssertChains(t, events)
			diags := diagnosticsOf(events)
			if len(diags) != 2 {
				t.Fatalf("diagnostic events = %v, want both findings", diags)
			}
			for _, d := range diags {
				if d.Fields["code"] == "DL3006" && (d.Fields["reported"] != false || d.Fields["gates"] != false) {
					t.Errorf("the warning = %v, want reported and gates false", d.Fields)
				}
				if d.Tool == "broken" {
					t.Errorf("a synthetic finding was emitted: %v", d.Fields)
				}
			}
		})
	}

	t.Run("invalid", func(t *testing.T) {
		e := eventsProject(t)
		res := e.run("", []string{"DATAMITSU_EVENTS=everything"}, "lint")
		e.wantExit(res, 2)
		e.golden("events_invalid_env", res)
	})
}

// A finished task says how many findings it has per level, zero included,
// and whether a cache answered for it; no other event carries counters.
func TestEventsCounters(t *testing.T) {
	e := eventsProject(t)
	res := e.run("", nil, jsonl("lint", "--fail-fast=false")...)
	e.wantExit(res, 1)
	counters := []string{"findings_error", "findings_warning", "findings_info", "findings_hint", "cached"}
	for _, ev := range clitest.MustParseJSONL(t, res.Stderr) {
		terminal := ev.Type == "tool_run" && (ev.Status == "done" || ev.Status == "fail")
		for _, key := range counters {
			if ev.Has(key) != terminal {
				t.Errorf("%s %s event carries %s: %v, want it only on a finished tool_run", ev.Type, ev.Status, key, ev.Has(key))
			}
		}
		if !terminal {
			continue
		}
		want := map[string]string{"findings_error": "0", "findings_warning": "0", "findings_info": "0", "findings_hint": "0"}
		if ev.Tool == "hadolint" {
			want["findings_error"], want["findings_warning"] = "1", "1"
		}
		for key, n := range want {
			if got := fmt.Sprint(ev.Fields[key]); got != n {
				t.Errorf("%s: %s = %s, want %s", ev.Tool, key, got, n)
			}
		}
		if ev.Fields["cached"] != false {
			t.Errorf("%s: cached = %v, want false", ev.Tool, ev.Fields["cached"])
		}
	}
}

// A stream that cannot be written fails the command with exit 1 — a run that
// passed, a plan, any other command — and the error goes to stdout: a broken
// stderr is not an unwritten report.
func TestEventsBrokenStream(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is Linux's; the property that a failed stream fails the run is left unverified here")
	}
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
		clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
	for _, args := range [][]string{{"lint"}, {"lint", "--explain"}, {"config", "runtime"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
			if err != nil {
				t.Skipf("/dev/full: %v", err)
			}
			t.Cleanup(func() { _ = full.Close() })

			var stdout bytes.Buffer
			cmd := exec.Command(clitest.BuildOnce(t), append([]string{"--no-auto-config", "--config", e.cfg, "--log-format", "jsonl"}, args...)...)
			cmd.Dir = e.p.Dir
			cmd.Env = clitest.BaseEnv(e.cache)
			cmd.Stdout, cmd.Stderr = &stdout, full
			err = cmd.Run()
			if code := clitest.ExitCodeOf(err); code != 1 {
				t.Fatalf("exit code = %d, want 1\n%s", code, stdout.String())
			}
			if !strings.Contains(stdout.String(), "error: the JSON-L event stream could not be written") {
				t.Errorf("stdout = %q, want the stream's error", stdout.String())
			}
		})
	}

	// A report on stdout owns it — from a run or from report render: the
	// command still fails, and writes nothing after the report.
	e.p.WriteFile("run.json", `{"schema": "datamitsu.report/1", "selection": {"mode": "all"}}`)
	for _, args := range [][]string{
		{"lint", "--report", "json=-"},
		{"--log-format", "jsonl", "report", "render", "--input", "run.json", "--format", "json"},
	} {
		t.Run("stdout_"+args[0], func(t *testing.T) {
			stdout, code := runWithFullStderr(t, e, args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			var doc map[string]any
			if err := json.Unmarshal(stdout, &doc); err != nil {
				t.Errorf("stdout is not the report alone: %v\n%s", err, stdout)
			}
		})
	}

	// A broken stream outranks a report that could not be written: exit 1.
	t.Run("export_failed", func(t *testing.T) {
		e.p.WriteFile("blocker", "a file, not a directory\n")
		stdout, code := runWithFullStderr(t, e, "--log-format", "jsonl", "lint", "--report", "json=blocker/x.json")
		if code != 1 || !strings.Contains(string(stdout), "report json: blocker/x.json: not a directory") ||
			!strings.Contains(string(stdout), "the JSON-L event stream could not be written") {
			t.Errorf("exit code = %d, stdout %q; want 1 naming both failures", code, stdout)
		}
	})
}

// runWithFullStderr runs the binary in e with stderr on /dev/full and returns
// its stdout and exit code.
func runWithFullStderr(t *testing.T, e *execProject, args ...string) ([]byte, int) {
	t.Helper()
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("/dev/full: %v", err)
	}
	t.Cleanup(func() { _ = full.Close() })
	var stdout bytes.Buffer
	cmd := exec.Command(clitest.BuildOnce(t), append([]string{"--no-auto-config", "--config", e.cfg}, args...)...)
	cmd.Dir = e.p.Dir
	cmd.Env = clitest.BaseEnv(e.cache)
	cmd.Stdout, cmd.Stderr = &stdout, full
	code := clitest.ExitCodeOf(cmd.Run())
	return stdout.Bytes(), code
}

// A secret value is masked everywhere in the stream, as it is in a report —
// here it is a directory name, which every event of the task carries in its
// op_id — and the events of one task still share one op_id.
func TestEventsMaskSecrets(t *testing.T) {
	const secret = "abcdefgh12"
	e := newExecProject(t, map[string]string{"fixture.marker": "", secret + "/Dockerfile": "FROM debian\n"}, fixtureSpec)
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, currentParserModule)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, parsedTool(hadolintFinding, 0)))
	res := e.run("", []string{"DATAMITSU_TEST_TOKEN=" + secret}, jsonl("lint", "--events", "diagnostics=all", "--report", "json=run.json")...)
	e.wantExit(res, 0)
	events := clitest.MustParseJSONL(t, res.Stderr)
	clitest.AssertChains(t, events)
	diags := diagnosticsOf(events)
	if len(diags) != 1 || diags[0].Fields["file"] != "***/Dockerfile" || diags[0].Dir != "***" {
		t.Fatalf("diagnostic events = %v, want one in ***/Dockerfile", diags)
	}
	if run := toolRuns(events, "hadolint"); diags[0].OpID != run[0].OpID {
		t.Errorf("diagnostic op_id = %s, want its task's %s", diags[0].OpID, run[0].OpID)
	}
	if strings.Contains(res.Stderr, secret) || strings.Contains(e.read("run.json"), secret) {
		t.Errorf("the stream or the report holds the secret:\n%s", res.Stderr)
	}
}

// findingFingerprints maps the code of each finding of a report document to its
// fingerprint.
func findingFingerprints(doc map[string]any) map[string]string {
	out := map[string]string{}
	data, _ := json.Marshal(doc)
	var run struct {
		Operations []struct {
			Tools []struct {
				Invocations []struct {
					Findings []struct {
						Code        string `json:"code"`
						Fingerprint string `json:"fingerprint"`
					} `json:"findings"`
				} `json:"invocations"`
			} `json:"tools"`
		} `json:"operations"`
	}
	_ = json.Unmarshal(data, &run)
	for _, op := range run.Operations {
		for _, tool := range op.Tools {
			for _, inv := range tool.Invocations {
				for _, f := range inv.Findings {
					out[f.Code] = f.Fingerprint
				}
			}
		}
	}
	return out
}
