package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// renderFormats are the formats `report render` writes from a run's own JSON,
// with the file each is written to.
var renderFormats = []struct {
	format, file string
	companion    bool
}{
	{companion: false, format: "markdown", file: "run.md"},
	{companion: false, format: "sarif", file: "results.sarif"},
	{format: "junit", file: "junit.xml", companion: true},
	{format: "codequality", file: "gl-code-quality.json", companion: true},
	{format: "checkstyle", file: "checkstyle.xml", companion: true},
	{format: "rdjsonl", file: "rd.jsonl", companion: true},
}

// TestReportRenderFormats: every format `report render` writes from a run's
// own JSON is the file the run wrote, byte for byte, companion included — on
// another machine, without the checkout, after the sources changed.
func TestReportRenderFormats(t *testing.T) {
	e := reportProject(t, outsider, clitest.ShellTool("beta", failScript, clitest.ToolOpSpec{Operation: "", Scope: "", Args: nil, Globs: nil, Priority: 0, ProjectTypes: nil, Parser: "", ParserModule: "", Env: nil, InheritEnv: nil, FailOn: "", Input: "", Output: ""}))
	args := make([]string, 0, 3+2*len(renderFormats))
	args = append(args, "lint", "--report", "json=out/run.json")
	for _, f := range renderFormats {
		args = append(args, "--report", f.format+"=out/"+f.file)
	}
	e.wantExit(e.run("", nil, args...), 1)

	// Another machine: a directory outside any repository holding the
	// document alone, and a checkout whose sources changed.
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "run.json"), []byte(e.read("out/run.json")), 0o644); err != nil {
		t.Fatal(err)
	}
	e.p.WriteFile("Dockerfile", "FROM debian:12\nWORKDIR /app\n")

	for _, f := range renderFormats {
		t.Run(f.format, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Env: nil, Stdin: "", Timeout: 0, Dir: elsewhere, CacheDir: e.cache},
				"report", "render", "--input", "run.json", "--format", f.format, "--output", "again/"+f.file)
			e.wantExit(res, 0)
			again, err := os.ReadFile(filepath.Join(elsewhere, "again", f.file))
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != e.read("out/"+f.file) {
				t.Errorf("report render wrote another %s than the run:\n--- run\n%s\n--- render\n%s", f.format, e.read("out/"+f.file), again)
			}
			companion, err := os.ReadFile(filepath.Join(elsewhere, "again", f.file+".completeness.json"))
			switch {
			case f.companion && err != nil:
				t.Errorf("no companion beside the rendered %s: %v", f.format, err)
			case f.companion && string(companion) != e.read("out/"+f.file+".completeness.json"):
				t.Errorf("report render wrote another companion than the run:\n%s", companion)
			case !f.companion && err == nil:
				t.Errorf("%s has a companion", f.format)
			}

			stdout := clitest.Run(t, clitest.RunOptions{Env: nil, Stdin: "", Timeout: 0, Dir: elsewhere, CacheDir: e.cache},
				"report", "render", "--input", "run.json", "--format", f.format)
			e.wantExit(stdout, 0)
			if stdout.Stdout != e.read("out/"+f.file) {
				t.Errorf("report render to stdout wrote another %s", f.format)
			}
		})
	}
}

func TestReportRenderSARIFOmitContradictoryTool(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("run.json", `{
  "schema":"datamitsu.report/1", "selection":{"mode":"all"}, "complete":true, "incomplete":[],
  "operations":[{"name":"lint", "ran":true, "tools":[
    {"name":"partial", "complete":true, "incomplete":["not-started"]},
    {"name":"pending", "complete":true, "incomplete":[], "invocations":[{"state":"not-started"}]},
    {"name":"unparsed", "complete":true, "incomplete":[], "invocations":[{"state":"ran", "extraction":"parse-failed"}]},
    {"name":"empty", "complete":true, "incomplete":[], "invocations":[]},
    {"name":"ready", "complete":true, "incomplete":[], "invocations":[{"state":"ran", "extraction":"parsed-clean", "exitCode":0, "success":true}]}
  ]}]
}`)
	res := clitest.Run(t, clitest.RunOptions{
		CacheDir: "",
		Env:      nil,
		Stdin:    "",
		Timeout:  0,
		Dir:      p.Dir,
	}, "report", "render", "--input", "run.json", "--format", "sarif")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, `"ready"`) || strings.Contains(res.Stdout, `"partial"`) || strings.Contains(res.Stdout, `"pending"`) || strings.Contains(res.Stdout, `"unparsed"`) || strings.Contains(res.Stdout, `"empty"`) {
		t.Fatalf("expected only the complete tool in SARIF: exit=%d\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestReportRenderRejectsContradictoryCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement, reason string
	}{
		{"narrowed", `"mode":"all"`, `"mode":"paths"`, "narrowed-selection"},
		{"all with paths", `"mode":"all"`, `"mode":"all","paths":["a.go"]`, "narrowed-selection"},
		{"all with directory", `"mode":"all"`, `"mode":"all","dir":"pkg"`, "narrowed-selection"},
		{"unstarted operation", `"ran":true`, `"ran":false`, "operation-skipped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			p.WriteFile("run.json", strings.Replace(equivalentCompleteReport, tc.old, tc.replacement, 1))
			opts := clitest.RunOptions{
				CacheDir: "",
				Env:      nil,
				Stdin:    "",
				Timeout:  0,
				Dir:      p.Dir,
			}
			res := clitest.Run(t, opts, "report", "render", "--input", "run.json", "--format", "sarif")
			if res.ExitCode != 0 || res.Stdout != "" || !strings.Contains(res.Stderr, tc.reason) {
				t.Fatalf("SARIF should omit the tool for %s: %+v", tc.reason, res)
			}
			if tc.reason == "narrowed-selection" {
				res = clitest.Run(t, opts, "report", "render", "--input", "run.json", "--format", "markdown")
				if res.ExitCode != 2 || !strings.Contains(res.Stderr, "--allow-partial") {
					t.Fatalf("Markdown should require --allow-partial: %+v", res)
				}
			}
		})
	}
}

const equivalentCompleteReport = `{
  "schema":"datamitsu.report/1", "selection":{"mode":"all"}, "complete":true, "incomplete":[],
  "operations":[{"name":"lint", "ran":true, "tools":[
    {"name":"ready", "complete":true, "incomplete":[], "invocations":[{"state":"verdict-hit", "extraction":"parsed-clean", "success":true, "files":[{"path":"a.go","state":"verdict-hit","success":true}]}]}
  ]}]
}`

func TestReportRenderRedactedVerdictAndSchema(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("run.json", equivalentCompleteReport)
	masked := clitest.RunOptions{
		CacheDir: "",
		Stdin:    "",
		Timeout:  0,
		Dir:      p.Dir, Env: []string{"REPORT_TOKEN=verdict-hit", "SCHEMA_SECRET=datamitsu.report/1"},
	}
	res := clitest.Run(t, masked, "report", "render", "--input", "run.json", "--format", "json", "--output", "redacted.json")
	if res.ExitCode != 0 {
		t.Fatalf("JSON rendering failed: %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, "redacted.json"))
	if err != nil || strings.Contains(string(data), "verdict-hit") {
		t.Fatalf("expected JSON with masked verdict: %v\n%s", err, data)
	}
	res = clitest.Run(t, clitest.RunOptions{
		CacheDir: "",
		Env:      nil,
		Stdin:    "",
		Timeout:  0,
		Dir:      p.Dir,
	}, "report", "render", "--input", "redacted.json", "--format", "sarif")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, `"name": "ready"`) || !strings.Contains(res.Stdout, `"executionSuccessful": true`) {
		t.Fatalf("expected successful cached tool in replay SARIF: %+v", res)
	}
	for _, tc := range []struct{ format, filename, expected string }{
		{"junit", "out.xml", `<testcase classname="ready" name="a.go"`},
		{"markdown", "out.md", "| 0 | 1 |"},
		{"history", "history.jsonl", `"cached":1`},
	} {
		t.Run(tc.format, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{
				CacheDir: "",
				Env:      nil,
				Stdin:    "",
				Timeout:  0,
				Dir:      p.Dir,
			}, "report", "render", "--input", "redacted.json", "--format", tc.format, "--output", tc.filename)
			data, err := os.ReadFile(filepath.Join(p.Dir, tc.filename))
			if res.ExitCode != 0 || err != nil || !strings.Contains(string(data), tc.expected) {
				t.Fatalf("expected the cached file in %s: %+v, %v\n%s", tc.format, res, err, data)
			}
			if tc.format == "junit" && !strings.Contains(string(data), `name="cached" value="1"`) {
				t.Fatalf("JUnit lost the cached file count:\n%s", data)
			}
		})
	}
}

func TestReportRenderSARIFWithRedactedExtraction(t *testing.T) {
	e := newParsedProject(t, "[]", 0)
	const secret = "parsed-clean"
	e.wantExit(e.run("", []string{"REPORT_TOKEN=" + secret}, "lint", "--report", "json=run.json", "--report", "sarif=live.sarif"), 0)
	if data := e.read("run.json"); strings.Contains(data, secret) || !strings.Contains(data, `"extraction": "***"`) {
		t.Fatalf("expected masked extraction in run JSON: %s", data)
	}
	res := e.run("", nil, "report", "render", "--input", "run.json", "--format", "sarif")
	e.wantExit(res, 0)
	if res.Stdout != e.read("live.sarif") {
		t.Fatalf("replay changed live SARIF: %s", res.Stdout)
	}
}

func TestReportRenderPreservesRedactedCompletenessReason(t *testing.T) {
	e := newParsedProject(t, "[]", 2)
	const secret = "failed-without-findings"
	e.wantExit(e.run("", []string{"REPORT_TOKEN=" + secret}, "lint", "--report", "json=run.json"), 1)
	original := e.read("run.json")
	if strings.Contains(original, secret) || !strings.Contains(original, `"***"`) {
		t.Fatalf("expected the producer to mask the reason: %s", original)
	}
	res := e.run("", []string{"REPORT_TOKEN="}, "report", "render", "--input", "run.json", "--format", "json")
	e.wantExit(res, 0)
	if strings.Contains(res.Stdout+res.Stderr, secret) || res.Stdout != original {
		t.Fatalf("offline replay changed the masked report:\n%s\n%s", res.Stdout, res.Stderr)
	}
}

func TestReportRenderRedactedJSONToSARIF(t *testing.T) {
	e := newParsedProject(t, "[]", 0)
	const secret = "parsed-clean"
	e.wantExit(e.run("", []string{"REPORT_TOKEN="}, "lint", "--report", "json=run.json"), 0)
	if !strings.Contains(e.read("run.json"), `"extraction": "parsed-clean"`) {
		t.Fatal("expected unmasked extraction in the original report")
	}
	args := []string{"report", "render", "--input", "run.json"}
	env := []string{"REPORT_TOKEN=" + secret}
	want := e.run("", env, append(args, "--format", "sarif")...)
	e.wantExit(want, 0)
	if !strings.Contains(want.Stdout, `"name": "hadolint"`) {
		t.Fatalf("direct SARIF omitted hadolint: %s", want.Stdout)
	}
	e.wantExit(e.run("", env, append(args, "--format", "json", "--output", "redacted.json")...), 0)
	if data := e.read("redacted.json"); strings.Contains(data, secret) {
		t.Fatalf("redacted JSON retained the masked value: %s", data)
	}
	got := e.run("", []string{"REPORT_TOKEN="}, "report", "render", "--input", "redacted.json", "--format", "sarif")
	e.wantExit(got, 0)
	if got.Stdout != want.Stdout {
		t.Fatalf("intermediate JSON changed SARIF:\n--- direct\n%s\n--- replay\n%s", want.Stdout, got.Stdout)
	}
}

func TestReportRenderMasksSecretsWithoutChangingOutputPath(t *testing.T) {
	e := reportProject(t)
	e.wantExit(e.run("", nil, "lint", "--report", "json=run.json"), 0)
	secret := "report-render-secret-value"
	res := clitest.Run(t, clitest.RunOptions{
		Stdin:   "",
		Timeout: 0,

		Dir: e.p.Dir, CacheDir: e.cache,
		Env: []string{"REPORT_TOKEN=" + secret},
	},
		"report", "render", "--input", "run.json", "--format", "json", "--output", "out/"+secret+".json")
	e.wantExit(res, 0)
	if strings.Contains(res.Stdout+res.Stderr, secret) {
		t.Fatalf("secret leaked to CLI output: %s%s", res.Stdout, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(e.p.Dir, "out", secret+".json")); err != nil {
		t.Fatalf("render output path was changed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(e.p.Dir, "out", secret+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("secret leaked in rendered report: %s", data)
	}
}

// TestReportRenderNarrowedFormats: a document of a narrowed run is refused for
// every format that lists findings unless --allow-partial, and written for
// SARIF, which leaves its incomplete tools out.
func TestReportRenderNarrowedFormats(t *testing.T) {
	e := reportProject(t)
	e.wantExit(e.run("", nil, "lint", "Dockerfile", "--allow-partial", "--report", "json=run.json"), 0)
	for _, f := range renderFormats {
		t.Run(f.format, func(t *testing.T) {
			res := e.run("", nil, "report", "render", "--input", "run.json", "--format", f.format, "--output", "out/"+f.file)
			if f.format == "sarif" {
				// Not refused, and not written either: every tool is left
				// out, and code scanning refuses a file without a run.
				e.wantExit(res, 0)
				if _, err := os.Stat(filepath.Join(e.p.Dir, "out", f.file)); !os.IsNotExist(err) {
					t.Errorf("the SARIF of a narrowed run was written: %v", err)
				}
				if !strings.Contains(res.Stderr, "sarif is not written") {
					t.Errorf("stderr:\n%s", res.Stderr)
				}
				return
			}
			e.wantExit(res, 2)
			if !strings.Contains(res.Stderr, "pass --allow-partial") {
				t.Errorf("stderr:\n%s", res.Stderr)
			}
			e.wantExit(e.run("", nil, "report", "render", "--input", "run.json", "--format", f.format, "--output", "out/"+f.file, "--allow-partial"), 0)
		})
	}
}

func TestReportSARIFCleanupFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("fixture requires Unix directory write permissions")
	}
	e := reportProject(t)
	const old = "previous SARIF"
	e.p.WriteFile("split/datamitsu-1.sarif", old)
	dir := filepath.Join(e.p.Dir, "split")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()
	live := e.run("", nil, jsonl("lint", "Dockerfile", "--allow-partial", "--report", "sarif=split/", "--report", "json=run.json")...)
	e.wantExit(live, 5)
	events := clitest.MustParseJSONL(t, live.Stderr)
	wantReportEvent(t, eventsOf(events, func(e clitest.Event) bool { return e.Fields["format"] == "sarif" }),
		"sarif", "split/", "failed", "clear previous report")
	replay := e.run("", nil, "report", "render", "--input", "run.json", "--format", "sarif", "--output", "split/")
	e.wantExit(replay, 5)
	if !strings.Contains(replay.Stderr, "clear previous report") || e.read("split/datamitsu-1.sarif") != old {
		t.Fatalf("replay must report the failed deletion: %s", replay.Stderr)
	}
}
