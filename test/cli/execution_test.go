package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// This file freezes what check, fix and lint do when tools actually run: where
// fail-fast stops a run, what a failure prints, the JSON-L stream, the cache
// footer and the exit codes. The tools are sh scripts (clitest.ShellTool) that
// record every run in the project's marker directory. Where later plans of
// docs/plans/2026-09-26-unified-results.md change a scenario, its comment names
// them; each such plan changes the assertion, or adds a twin beside it, and
// regenerates the golden in the same change.

// settle keeps every tool process above one millisecond. A faster one reports a
// duration of 0, which omitempty drops from its JSON-L event, so whether the
// field is present would depend on the machine.
const settle = "sleep 0.01; "

const (
	passScript = settle + clitest.RecordRun
	failScript = settle + clitest.RecordRun + `; echo "$0: failed"; exit 1`
)

// fixtureTypes declares the project type most scenarios detect at the
// repository root through fixture.marker.
var fixtureTypes = map[string][]string{"fixture": {"fixture.marker"}}

var fixtureSpec = clitest.ShellConfigSpec{ProjectTypes: fixtureTypes}

// execProject is one scenario's repository, config and isolated cache. Runs of
// one execProject share the cache, which is what a cache scenario needs.
type execProject struct {
	t     *testing.T
	p     *clitest.Project
	cfg   string
	cache string
}

func newExecProject(t *testing.T, files map[string]string, spec clitest.ShellConfigSpec, tools ...string) *execProject {
	t.Helper()
	clitest.RequireShell(t, "the execution contract of check, fix and lint")
	p := clitest.NewProject(t)
	clitest.MarkerDir(p)
	for rel, content := range files {
		p.WriteFile(rel, content)
	}
	return &execProject{
		t:     t,
		p:     p,
		cfg:   p.WriteFile("exec.config.js", clitest.ShellConfig(spec, tools...)),
		cache: t.TempDir(),
	}
}

func (e *execProject) run(dir string, env []string, args ...string) clitest.Result {
	e.t.Helper()
	full := append([]string{"--no-auto-config", "--config", e.cfg}, args...)
	return clitest.Run(e.t, clitest.RunOptions{
		Dir:      filepath.Join(e.p.Dir, dir),
		CacheDir: e.cache,
		Env:      env,
	}, full...)
}

var (
	// Durations are masked by the normalizer, but the text around them still
	// depends on the value: a failure frame prints "12ms" below 100ms and
	// "0.12s (123ms)" above, and the per-tool line pads the duration to a
	// fixed column, so the spaces after the mask vary with what it hid.
	durPairRE    = regexp.MustCompile(`<DUR> \(<DUR>\)`)
	durPaddingRE = regexp.MustCompile(`<DUR> {2,}`)
	// Progress lines ("→ label n/total (pct%)") are throttled display: a line
	// is printed per 10% step or every two seconds, carrying whichever label
	// the last parallel callback set. They are not part of the results.
	progressRE = regexp.MustCompile(`(?m)^→ .*\n`)
)

func (e *execProject) normalize(s string, extra ...func(string) string) string {
	s = clitest.NewNormalizer().MaskPath(e.p.Dir, "<TMP>").MaskPath(e.cache, "<CACHE>").Apply(s)
	s = progressRE.ReplaceAllString(s, "")
	s = durPairRE.ReplaceAllString(s, "<DUR>")
	s = durPaddingRE.ReplaceAllString(s, "<DUR> ")
	for _, f := range extra {
		s = f(s)
	}
	return s
}

func (e *execProject) golden(name string, res clitest.Result, extra ...func(string) string) {
	e.t.Helper()
	clitest.AssertGolden(e.t, "execution_"+name, fmt.Sprintf("exit code: %d\n===STDOUT===\n%s\n===STDERR===\n%s",
		res.ExitCode, e.normalize(res.Stdout, extra...), e.normalize(res.Stderr, extra...)))
}

// goldenJSONL sorts the lines: parallel events arrive in no fixed order, and
// their causal order is asserted by AssertChains instead.
func (e *execProject) goldenJSONL(name string, res clitest.Result, extra ...func(string) string) {
	e.t.Helper()
	stream := clitest.NormalizeJSONL(res.Stderr)
	for _, f := range extra {
		stream = f(stream)
	}
	stream = clitest.NewNormalizer().MaskPath(e.p.Dir, "<TMP>").MaskPath(e.cache, "<CACHE>").SortLines().Apply(stream)
	clitest.AssertGolden(e.t, "execution_"+name, fmt.Sprintf("exit code: %d\n===STDOUT===\n%s\n===STDERR===\n%s",
		res.ExitCode, e.normalize(res.Stdout), stream))
}

func (e *execProject) wantExit(res clitest.Result, want int) {
	e.t.Helper()
	if res.ExitCode != want {
		e.t.Fatalf("exit code = %d, want %d\n--- stdout ---\n%s\n--- stderr ---\n%s",
			res.ExitCode, want, res.Stdout, res.Stderr)
	}
}

// wantMarker asserts the exact lines a tool recorded, with the project
// directory masked as <TMP>; "" means the tool never ran.
func (e *execProject) wantMarker(tool, want string) {
	e.t.Helper()
	ran, got := e.p.Marker(tool)
	got = strings.ReplaceAll(got, e.p.Dir, "<TMP>")
	switch {
	case want == "" && ran:
		e.t.Errorf("tool %s ran, want it never to run; it recorded:\n%s", tool, got)
	case want != "" && got != want:
		e.t.Errorf("tool %s recorded %q, want %q", tool, got, want)
	}
}

func (e *execProject) read(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.p.Dir, rel))
	if err != nil {
		e.t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func eventsOf(events []clitest.Event, pred func(clitest.Event) bool) []clitest.Event {
	var out []clitest.Event
	for _, e := range events {
		if pred(e) {
			out = append(out, e)
		}
	}
	return out
}

func toolRuns(events []clitest.Event, tool string) []clitest.Event {
	return eventsOf(events, func(e clitest.Event) bool { return e.Type == "tool_run" && e.Tool == tool })
}

func jsonl(args ...string) []string {
	return append([]string{"--log-format", "jsonl"}, args...)
}

// TestExecutionTwoToolsPass is S1: two repository-scoped tools that both pass.
func TestExecutionTwoToolsPass(t *testing.T) {
	files := map[string]string{"fixture.marker": ""}
	tools := []string{
		clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}),
		clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{}),
	}

	t.Run("console", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "lint")
		e.wantExit(res, 0)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "beta \n")
		e.golden("s1_lint_pass", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("lint")...)
		e.wantExit(res, 0)
		if res.Stdout != "" {
			t.Errorf("a JSON-L run wrote to stdout:\n%s", res.Stdout)
		}
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if phases := eventsOf(events, func(e clitest.Event) bool { return e.Type == "phase" }); len(phases) != 1 ||
			phases[0].Status != "start" || phases[0].Op != "lint" {
			t.Errorf("phase events = %+v, want one lint start", phases)
		}
		for _, tool := range []string{"alpha", "beta"} {
			if runs := toolRuns(events, tool); len(runs) != 2 || runs[0].Status != "start" || runs[1].Status != "done" {
				t.Errorf("tool_run events of %s = %+v, want a start and a done", tool, runs)
			}
		}
		done := eventsOf(events, func(e clitest.Event) bool { return e.Type == "done" })
		if len(done) != 1 || done[0].Status != "done" || done[0].Tools != 2 || done[0].Runs != 2 {
			t.Errorf("done events = %+v, want one done with tools 2 and runs 2", done)
		}
	})
}

// TestExecutionFailFastBetweenPriorities is S2 (and S12 on it): a failure at
// priority 10 stops the run before priority 20. Plan 2 keeps this default,
// reports beta as not started, and adds a --fail-fast=false twin that runs it.
func TestExecutionFailFastBetweenPriorities(t *testing.T) {
	files := map[string]string{"fixture.marker": ""}
	tools := []string{
		clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{Priority: 10}),
		clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{Priority: 20}),
	}

	t.Run("console", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "lint")
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "")
		e.golden("s2_lint_priority_fail_fast", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("lint")...)
		e.wantExit(res, 1)
		e.wantMarker("beta", "")
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if beta := eventsOf(events, func(e clitest.Event) bool { return e.Tool == "beta" }); len(beta) != 0 {
			t.Errorf("beta never runs, yet it has events: %+v", beta)
		}
		if alpha := toolRuns(events, "alpha"); len(alpha) != 2 || alpha[1].Status != "fail" ||
			alpha[1].Success == nil || *alpha[1].Success || !alpha[1].Has("duration_ms") {
			t.Errorf("tool_run events of alpha = %+v, want a start and a fail with success false and a duration", alpha)
		}
		done := eventsOf(events, func(e clitest.Event) bool { return e.Type == "done" })
		if len(done) != 1 || done[0].Status != "fail" || done[0].Tools != 1 || done[0].Runs != 1 || done[0].Failed != 1 {
			t.Errorf("done events = %+v, want one fail with tools 1, runs 1, failed 1", done)
		}
		e.goldenJSONL("s12_jsonl_s2", res)
	})
}

// TestExecutionFailFastBetweenSubGroups is S3: repository-scoped tasks always
// overlap, so two of them at one priority run one after the other, and a failure
// of the first stops the second. Plan 2 keeps this default and adds a
// --fail-fast=false twin that runs the second.
func TestExecutionFailFastBetweenSubGroups(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
		clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{}),
		clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{}),
	)
	res := e.run("", nil, "lint")
	e.wantExit(res, 1)
	e.wantMarker("alpha", "alpha \n")
	e.wantMarker("beta", "")
	e.golden("s3_lint_subgroup_fail_fast", res)
}

// packagesSpec detects two packages of one project type, pkg/a and pkg/b. Tasks
// in different project directories never overlap, so per-project tasks in the
// two packages run as parallel siblings. A glob that matches a file of one
// package alone confines a per-project tool to it.
//
// One type, not one per package: the header lists the detected types in map
// order, so a golden of a run that detects two would not be stable.
var packagesSpec = clitest.ShellConfigSpec{ProjectTypes: map[string][]string{"pkg": {"**/pkg.marker"}}}

var packagesFiles = map[string]string{
	"pkg/a/pkg.marker": "",
	"pkg/a/a.src":      "",
	"pkg/b/pkg.marker": "",
	"pkg/b/b.src":      "",
}

// betaSleeps records its start at once and its end three seconds later.
const betaSleeps = `echo "$0 started" >> "$MARKERS/$0"; sleep 3; echo "$0 done" >> "$MARKERS/$0"`

// alphaFailsOnceBetaStarted waits, at most two seconds, for beta's start and
// then fails: whatever order the shuffled worker pool picks, beta is running
// when alpha fails.
const alphaFailsOnceBetaStarted = settle + clitest.RecordRun +
	`; i=0; while [ ! -f "$MARKERS/beta" ] && [ $i -lt 40 ]; do sleep 0.05; i=$((i+1)); done` +
	`; echo "$0: failed"; exit 1`

// betaResultRE matches beta's result line or failure frame, not the mention of
// its marker in alpha's command.
var betaResultRE = regexp.MustCompile(`(?m)^┃ . beta\b|─ beta\b`)

// killWindow is how long after a run's start beta would have written its done
// marker had it survived; looking earlier could not tell a kill from a process
// still asleep.
const killWindow = 3500 * time.Millisecond

// TestExecutionFailFastKillsSiblings is S4 (and S12 on it): a failing task
// cancels its running parallel sibling. The sibling is killed and vanishes
// from the results, and its tool_run start is left without a terminal event.
// Plan 2 keeps this default but prints the sibling as cancelled, with a
// terminal event, and adds a --fail-fast=false twin in which it finishes; plan 3
// gives each task its own invocation ID.
func TestExecutionFailFastKillsSiblings(t *testing.T) {
	tools := []string{
		clitest.ShellTool("alpha", alphaFailsOnceBetaStarted,
			clitest.ToolOpSpec{Scope: "per-project", Globs: []string{"**/a.src"}}),
		clitest.ShellTool("beta", betaSleeps,
			clitest.ToolOpSpec{Scope: "per-project", Globs: []string{"**/b.src"}}),
	}
	workers := []string{"DATAMITSU_MAX_PARALLEL_WORKERS=2"}

	runKilled := func(t *testing.T, args ...string) (*execProject, clitest.Result) {
		t.Helper()
		e := newExecProject(t, packagesFiles, packagesSpec, tools...)
		start := time.Now()
		res := e.run("", workers, args...)
		time.Sleep(time.Until(start.Add(killWindow)))
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "beta started\n")
		return e, res
	}

	t.Run("console", func(t *testing.T) {
		e, res := runKilled(t, "lint")
		if out := e.normalize(res.Stdout); betaResultRE.MatchString(out) {
			t.Errorf("the killed sibling appears in the results:\n%s", out)
		}
		e.golden("s4_lint_sibling_killed", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e, res := runKilled(t, jsonl("lint")...)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events, "beta")
		if beta := toolRuns(events, "beta"); len(beta) != 1 || beta[0].Status != "start" || beta[0].Dir != "pkg/b" {
			t.Errorf("tool_run events of beta = %+v, want a single start in pkg/b", beta)
		}
		e.goldenJSONL("s12_jsonl_s4", res)
	})
}

// claimFirst fails for whichever task runs first and passes for any later one.
// The worker pool shuffles parallel tasks before scheduling them, so plan order
// cannot pick the first one; the scenario is symmetric instead, and its golden
// masks the package the shuffle picked.
const claimFirst = settle + `if [ -e "$MARKERS/$0" ]; then echo "$0 ${PWD##*/} later" >> "$MARKERS/$0"; exit 0; fi; ` +
	`echo "$0 ${PWD##*/} first" >> "$MARKERS/$0"; echo "$0: failed"; exit 1`

var pkgDirRE = regexp.MustCompile(`pkg/[ab]\b`)

func maskPkgDir(s string) string { return pkgDirRE.ReplaceAllString(s, "pkg/<FIRST>") }

// TestExecutionFailFastBeforeWorker is S4b: with a single worker, the sibling
// still waiting for the slot when the first task fails is cancelled before it
// starts and leaves no trace — no process, no result, no event. Plan 2 keeps
// this default but prints the sibling as not started, and adds a
// --fail-fast=false twin in which it runs.
func TestExecutionFailFastBeforeWorker(t *testing.T) {
	files, spec := packagesFiles, packagesSpec
	tool := clitest.ShellTool("claim", claimFirst, clitest.ToolOpSpec{Scope: "per-project"})
	workers := []string{"DATAMITSU_MAX_PARALLEL_WORKERS=1"}

	onlyFirst := func(e *execProject) (first, other string) {
		e.t.Helper()
		switch _, got := e.p.Marker("claim"); got {
		case "claim a first\n":
			return "pkg/a", "pkg/b"
		case "claim b first\n":
			return "pkg/b", "pkg/a"
		default:
			e.t.Fatalf("claim recorded %q, want exactly one first run and no later one", got)
			return "", ""
		}
	}

	t.Run("console", func(t *testing.T) {
		e := newExecProject(t, files, spec, tool)
		res := e.run("", workers, "lint")
		e.wantExit(res, 1)
		if _, other := onlyFirst(e); strings.Contains(e.normalize(res.Stdout), other) {
			t.Errorf("the cancelled sibling (%s) appears in the results:\n%s", other, res.Stdout)
		}
		e.golden("s4b_lint_sibling_never_started", res, maskPkgDir)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, files, spec, tool)
		res := e.run("", workers, jsonl("lint")...)
		e.wantExit(res, 1)
		first, other := onlyFirst(e)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if got := eventsOf(events, func(e clitest.Event) bool { return e.Dir == other }); len(got) != 0 {
			t.Errorf("the sibling in %s never started, yet it has events: %+v", other, got)
		}
		if runs := toolRuns(events, "claim"); len(runs) != 2 || runs[0].Dir != first || runs[1].Status != "fail" {
			t.Errorf("tool_run events = %+v, want a start and a fail in %s", runs, first)
		}
	})
}

// perFileLoopTool is one task over every .txt file whose argv carries {file}, so
// the executor runs a process per file; it fails on the files named bad*.
var perFileLoopTool = clitest.ShellTool("alpha",
	settle+clitest.RecordRun+`; case "${1##*/}" in bad*) echo "$0: ${1##*/} failed"; exit 1;; esac`,
	clitest.ToolOpSpec{Scope: "per-project", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}})

var perFileLoopFiles = map[string]string{
	"fixture.marker": "",
	"bad1.txt":       "bad\n",
	"bad2.txt":       "bad\n",
	"ok.txt":         "ok\n",
}

// TestExecutionFailFastBetweenFiles is S5 (and S12 on it): the per-file loop of
// one task stops at the first failing file. Plan 2 keeps this default and adds
// a --fail-fast=false twin that runs all three files.
func TestExecutionFailFastBetweenFiles(t *testing.T) {
	t.Run("console", func(t *testing.T) {
		e := newExecProject(t, perFileLoopFiles, fixtureSpec, perFileLoopTool)
		res := e.run("", nil, "lint")
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha <TMP>/bad1.txt\n")
		e.golden("s5_lint_per_file_loop", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, perFileLoopFiles, fixtureSpec, perFileLoopTool)
		res := e.run("", nil, jsonl("lint")...)
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha <TMP>/bad1.txt\n")
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		// One progress chunk for the first of three files, then the loop broke.
		chunks := eventsOf(events, func(e clitest.Event) bool { return e.Type == "chunk" })
		if len(chunks) != 1 || chunks[0].Index != 1 || chunks[0].Total != 3 || chunks[0].Status != "progress" ||
			chunks[0].Success == nil || *chunks[0].Success {
			t.Errorf("chunk events = %+v, want a single failed progress chunk 1/3", chunks)
		}
		errs := eventsOf(events, func(e clitest.Event) bool { return e.Type == "error" && e.Tool == "alpha" })
		if len(errs) != 1 || !strings.Contains(errs[0].Msg, "bad1.txt") {
			t.Errorf("error events of alpha = %+v, want one naming bad1.txt", errs)
		}
		e.goldenJSONL("s12_jsonl_s5", res)
	})
}

// TestExecutionCheckStopsAfterFix is S6: when fix fails, check never starts
// lint — no lint block, no lint phase event, and no closing line for the
// whole check. Plan 2 adds that closing line and a --fail-fast=false twin that
// runs lint after the failed fix.
func TestExecutionCheckStopsAfterFix(t *testing.T) {
	files := map[string]string{"fixture.marker": ""}
	tools := []string{
		clitest.ShellTool("fixer", failScript, clitest.ToolOpSpec{Operation: "fix"}),
		clitest.ShellTool("linter", passScript, clitest.ToolOpSpec{}),
	}

	t.Run("console", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "check")
		e.wantExit(res, 1)
		e.wantMarker("fixer", "fixer \n")
		e.wantMarker("linter", "")
		if strings.Contains(res.Stdout, "┏━ lint") {
			t.Errorf("check opened a lint block after a failed fix:\n%s", res.Stdout)
		}
		e.golden("s6_check_fix_fails", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("check")...)
		e.wantExit(res, 1)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if phases := eventsOf(events, func(e clitest.Event) bool { return e.Type == "phase" }); len(phases) != 1 ||
			phases[0].Op != "fix" {
			t.Errorf("phase events = %+v, want the fix phase only", phases)
		}
	})
}

// dropDurations removes duration_ms from a normalized JSON-L golden. A task
// served from the cache takes a millisecond or none depending on the machine,
// and omitempty drops the field when it is zero.
func dropDurations(s string) string { return strings.ReplaceAll(s, `"duration_ms":1,`, "") }

// TestExecutionPerFileCache is S7: a second lint over unchanged files is served
// from the per-file cache without running the tool. Plan 3 changes what a cached
// task reports.
func TestExecutionPerFileCache(t *testing.T) {
	e := newExecProject(t,
		map[string]string{"fixture.marker": "", "a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n"},
		fixtureSpec,
		clitest.ShellTool("alpha", passScript,
			clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}}),
	)

	cold := e.run("", nil, "lint")
	e.wantExit(cold, 0)
	_, recorded := e.p.Marker("alpha")
	if n := strings.Count(recorded, "\n"); n != 3 {
		t.Fatalf("the cold run recorded %d runs, want 3:\n%s", n, recorded)
	}
	e.golden("s7_lint_cache_cold", cold)

	warm := e.run("", nil, "lint")
	e.wantExit(warm, 0)
	if _, again := e.p.Marker("alpha"); again != recorded {
		t.Errorf("the warm run ran the tool again:\n%s", again)
	}
	if !strings.Contains(warm.Stdout, "cache 100%") {
		t.Errorf("the warm run's footer should report cache 100%%:\n%s", warm.Stdout)
	}
	e.golden("s7_lint_cache_warm", warm)

	events := e.run("", nil, jsonl("lint")...)
	e.wantExit(events, 0)
	if _, again := e.p.Marker("alpha"); again != recorded {
		t.Errorf("the warm JSON-L run ran the tool again:\n%s", again)
	}
	clitest.AssertChains(t, clitest.MustParseJSONL(t, events.Stderr))
	e.goldenJSONL("s7_jsonl_cache_warm", events, dropDurations)
}

// hadolintFinding is one finding in hadolint's --format=json shape.
const hadolintFinding = `[{"file":"Dockerfile","line":1,"column":1,"level":"warning","code":"DL3006",` +
	`"message":"Always tag the version of an image explicitly"}]`

// parsedTool lints each Dockerfile, prints hadolintFinding and exits with
// exitCode; its output goes through the hadolint parser of the seeded module.
func parsedTool(exitCode int) string {
	return clitest.ShellTool("hadolint",
		fmt.Sprintf("%s%s; echo '%s'; exit %d", settle, clitest.RecordRun, hadolintFinding, exitCode),
		clitest.ToolOpSpec{
			Scope:  "per-file",
			Globs:  []string{"**/Dockerfile"},
			Args:   []string{"{file}"},
			Parser: "hadolint",
		})
}

// newParsedProject seeds the committed crate build of the parser module into
// the scenario's cache before writing a config that declares it.
func newParsedProject(t *testing.T, exitCode int) *execProject {
	t.Helper()
	e := newExecProject(t, map[string]string{"fixture.marker": "", "Dockerfile": "FROM debian\n"}, fixtureSpec)
	module := filepath.Join("..", "..", "internal", "parsermanager", "testdata", "echo.wasm")
	spec := fixtureSpec
	spec.Parsers = clitest.SeedParserModule(t, e.cache, module)
	e.p.WriteFile("exec.config.js", clitest.ShellConfig(spec, parsedTool(exitCode)))
	return e
}

// TestExecutionParsedFailure is S8: a failing tool with an outputParser shows
// the parsed findings in its frame, and --no-parse shows the raw output. Plan 3
// changes how a parsed result is recorded.
func TestExecutionParsedFailure(t *testing.T) {
	t.Run("parsed", func(t *testing.T) {
		e := newParsedProject(t, 1)
		res := e.run("", nil, "lint")
		e.wantExit(res, 1)
		if !strings.Contains(res.Stdout, "Dockerfile:1:1 warning Always tag the version of an image explicitly [DL3006]") {
			t.Errorf("the frame should show the parsed finding:\n%s", res.Stdout)
		}
		e.golden("s8_lint_parsed_failure", res)
	})

	t.Run("no-parse", func(t *testing.T) {
		e := newParsedProject(t, 1)
		res := e.run("", nil, "lint", "--no-parse")
		e.wantExit(res, 1)
		if !strings.Contains(res.Stdout, hadolintFinding) {
			t.Errorf("--no-parse should show the raw output:\n%s", res.Stdout)
		}
		e.golden("s8_lint_parsed_failure_no_parse", res)
	})
}

// TestExecutionParsedPassHidesFindings is S9: a tool that reports a finding but
// exits 0 prints nothing about it, and its second run is a cache hit that does
// not run it at all. Plan 3 (C1) keeps the findings of a passing run.
func TestExecutionParsedPassHidesFindings(t *testing.T) {
	e := newParsedProject(t, 0)

	first := e.run("", nil, "lint")
	e.wantExit(first, 0)
	e.wantMarker("hadolint", "hadolint <TMP>/Dockerfile\n")
	if strings.Contains(first.Stdout, "DL3006") {
		t.Errorf("a passing run printed its finding:\n%s", first.Stdout)
	}
	e.golden("s9_lint_parsed_pass", first)

	second := e.run("", nil, "lint")
	e.wantExit(second, 0)
	e.wantMarker("hadolint", "hadolint <TMP>/Dockerfile\n")
	if !strings.Contains(second.Stdout, "cache 100%") {
		t.Errorf("the second run's footer should report cache 100%%:\n%s", second.Stdout)
	}
	e.golden("s9_lint_parsed_pass_cached", second)
}

var hostRE = regexp.MustCompile(`no binary for [a-z0-9]+/[a-z0-9_]+/[a-z0-9_]+`)

func maskHost(s string) string { return hostRE.ReplaceAllString(s, "no binary for <HOST>") }

// TestExecutionFailOnSkip is S10: --fail-on-skip turns a tool whose binary has
// no build for this host into exit 1. Plan 2 gives it its own exit code.
func TestExecutionFailOnSkip(t *testing.T) {
	otherOS := "windows"
	if runtime.GOOS == otherOS {
		otherOS = "linux"
	}
	binary := fmt.Sprintf(`c.apps["native"] = { binary: { binaries: { %s: { amd64: { unknown: {
  url: "https://example.invalid/native", contentType: "raw",
  hash: "3f79bb7b435b05321651daefd374cdc681dc06faa65e374e38337b88ca046dea" } } } } } };
c.tools["native"] = { name: "native", operations: { lint: { app: "native", args: [], scope: "repository" } } };
`, otherOS)
	e := newExecProject(t, map[string]string{"fixture.marker": ""},
		clitest.ShellConfigSpec{ProjectTypes: fixtureTypes, Extra: binary},
		clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}),
	)
	res := e.run("", nil, "lint", "--fail-on-skip")
	e.wantExit(res, 1)
	e.wantMarker("alpha", "alpha \n")
	if !strings.Contains(res.Stdout, "⊘ native") {
		t.Errorf("the skipped tool should be listed:\n%s", res.Stdout)
	}
	e.golden("s10_lint_fail_on_skip", res, maskHost)
}

// TestExecutionRequireCoverage is S11: --require-coverage=repo from a
// subdirectory runs the narrowed plan and then exits 4.
func TestExecutionRequireCoverage(t *testing.T) {
	e := newExecProject(t, packagesFiles, packagesSpec,
		clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{Scope: "per-project"}),
	)
	res := e.run("pkg/a", nil, "lint", "--require-coverage=repo")
	e.wantExit(res, 4)
	e.wantMarker("alpha", "alpha \n")
	if !strings.Contains(res.Stderr, "--require-coverage=repo") {
		t.Errorf("stderr should name the failed assertion:\n%s", res.Stderr)
	}
	e.golden("s11_lint_require_coverage", res)
}

// TestExecutionUsageErrors is S13: a malformed invocation exits 1 with an error
// line. Plan 2 gives usage errors exit code 2.
func TestExecutionUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"bogus_flag", []string{"lint", "--bogus-flag"}},
		{"widen_to_invalid", []string{"lint", "--widen-to=Repo"}},
		{"require_coverage_with_tools", []string{"lint", "--require-coverage=unit", "--tools", "alpha"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
				clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
			res := e.run("", nil, tc.args...)
			e.wantExit(res, 1)
			e.wantMarker("alpha", "")
			if !strings.Contains(res.Stderr, "error:") {
				t.Errorf("stderr should carry an error line:\n%s", res.Stderr)
			}
			e.golden("s13_"+tc.name, res)
		})
	}
}

// TestExecutionFixCache is S14: a fix that rewrites its file records the
// rewritten content, so the next fix is a cache hit; check then runs lint after
// the cached fix. Plan 3 (C2) changes what the fix cache records.
func TestExecutionFixCache(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": "", "a.txt": "original\n"}, fixtureSpec,
		clitest.ShellTool("appender", settle+clitest.RecordRun+`; echo appended >> "$1"`,
			clitest.ToolOpSpec{Operation: "fix", Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}}),
		clitest.ShellTool("linter", passScript,
			clitest.ToolOpSpec{Scope: "per-file", Globs: []string{"**/*.txt"}, Args: []string{"{file}"}}),
	)
	const fixed = "original\nappended\n"

	first := e.run("", nil, "fix")
	e.wantExit(first, 0)
	e.wantMarker("appender", "appender <TMP>/a.txt\n")
	if got := e.read("a.txt"); got != fixed {
		t.Errorf("a.txt after the first fix = %q, want %q", got, fixed)
	}
	e.golden("s14_fix_cold", first)

	second := e.run("", nil, "fix")
	e.wantExit(second, 0)
	e.wantMarker("appender", "appender <TMP>/a.txt\n")
	if got := e.read("a.txt"); got != fixed {
		t.Errorf("a.txt after the cached fix = %q, want %q", got, fixed)
	}
	e.golden("s14_fix_cached", second)

	// The lint footer of this check reports cache 50%: the hit and miss counters
	// span the whole process, so the lint operation's rate includes the fix
	// operation's hit.
	check := e.run("", nil, "check")
	e.wantExit(check, 0)
	e.wantMarker("appender", "appender <TMP>/a.txt\n")
	e.wantMarker("linter", "linter <TMP>/a.txt\n")
	e.golden("s14_check_after_fix", check)
}
