package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
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

const keepGoing = "--fail-fast=false"

// wantStopped asserts that a tool's only terminal event in dir is a skip with
// msg, and that it has a start event exactly when it was cancelled after it
// started.
func wantStopped(t *testing.T, events []clitest.Event, tool, dir, msg string) {
	t.Helper()
	var starts, skips int
	for _, e := range toolRuns(events, tool) {
		if e.Dir != dir {
			continue
		}
		switch e.Status {
		case "start":
			starts++
		case "skip":
			skips++
			if e.Msg != msg || e.Has("success") {
				t.Errorf("skip event of %s in %q = %+v, want msg %q and no success field", tool, dir, e, msg)
			}
		default:
			t.Errorf("%s in %q was stopped, yet it has a %s event: %+v", tool, dir, e.Status, e)
		}
	}
	wantStarts := 0
	if strings.HasPrefix(msg, "cancelled: ") {
		wantStarts = 1
	}
	if skips != 1 || starts != wantStarts {
		t.Errorf("%s in %q has %d start(s) and %d skip(s), want %d and 1", tool, dir, starts, skips, wantStarts)
	}
}

// operationDone matches the done event of one operation; the run-level done
// of the whole command has an op_id starting with "cmd-".
func operationDone(e clitest.Event) bool {
	return e.Type == "done" && strings.HasPrefix(e.OpID, "run-")
}

// runDone returns the one run-level done event of the stream.
func runDone(t *testing.T, events []clitest.Event) clitest.Event {
	t.Helper()
	done := eventsOf(events, func(e clitest.Event) bool { return e.Type == "done" && strings.HasPrefix(e.OpID, "cmd-") })
	if len(done) != 1 {
		t.Fatalf("run-level done events = %+v, want one", done)
	}
	return done[0]
}

// wantRunDone asserts the run-level done of a command: its op, outcome, totals
// and completeness.
func wantRunDone(t *testing.T, events []clitest.Event, op string, success bool, runs, cancelled int, complete bool) {
	t.Helper()
	d := runDone(t, events)
	switch {
	case d.Op != op, d.Success == nil, *d.Success != success, d.Runs != runs,
		d.Cancelled == nil, *d.Cancelled != cancelled, d.Complete == nil, *d.Complete != complete,
		!d.Has("duration_ms"):
		t.Errorf("run-level done = %+v, want op %s, success %v, runs %d, cancelled %d, complete %v and a duration",
			d.Fields, op, success, runs, cancelled, complete)
	}
}

// wantDone asserts the one per-operation done event's run and cancelled counts.
func wantDone(t *testing.T, events []clitest.Event, op string, runs, cancelled int) {
	t.Helper()
	done := eventsOf(events, func(e clitest.Event) bool { return e.Type == "done" && e.Op == op && strings.HasPrefix(e.OpID, "run-") })
	if len(done) != 1 {
		t.Fatalf("done events of %s = %+v, want one", op, done)
	}
	got := 0
	if done[0].Cancelled != nil {
		got = *done[0].Cancelled
	}
	if done[0].Runs != runs || got != cancelled || (cancelled == 0 && done[0].Has("cancelled")) {
		t.Errorf("done of %s = %+v, want runs %d and cancelled %d", op, done[0], runs, cancelled)
	}
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
		done := eventsOf(events, operationDone)
		if len(done) != 1 || done[0].Status != "done" || done[0].Tools != 2 || done[0].Runs != 2 {
			t.Errorf("done events = %+v, want one done with tools 2 and runs 2", done)
		}
		wantRunDone(t, events, "lint", true, 2, 0, true)
		if d := runDone(t, events); d.Tools != 2 || d.Status != "done" {
			t.Errorf("run-level done = %+v, want status done and tools 2", d.Fields)
		}
	})
}

// TestExecutionFailFastBetweenPriorities is S2 (and S12 on it): a failure at
// priority 10 stops the run before priority 20, and beta is reported as not
// started. --fail-fast=false, or DATAMITSU_FAIL_FAST=false, runs it; the flag
// wins over the variable.
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
		if !strings.Contains(res.Stdout, "⊘ beta   not started (fail-fast)") || !strings.Contains(res.Stdout, "· 1 cancelled") {
			t.Errorf("beta should be listed as not started and counted as cancelled:\n%s", res.Stdout)
		}
		e.golden("s2_lint_priority_fail_fast", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("lint")...)
		e.wantExit(res, 1)
		e.wantMarker("beta", "")
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantStopped(t, events, "beta", "", "not started: fail-fast")
		wantDone(t, events, "lint", 1, 1)
		if alpha := toolRuns(events, "alpha"); len(alpha) != 2 || alpha[1].Status != "fail" ||
			alpha[1].Success == nil || *alpha[1].Success || !alpha[1].Has("duration_ms") {
			t.Errorf("tool_run events of alpha = %+v, want a start and a fail with success false and a duration", alpha)
		}
		done := eventsOf(events, operationDone)
		if len(done) != 1 || done[0].Status != "fail" || done[0].Tools != 1 || done[0].Runs != 1 || done[0].Failed != 1 {
			t.Errorf("done events = %+v, want one fail with tools 1, runs 1, failed 1", done)
		}
		wantRunDone(t, events, "lint", false, 1, 1, false)
		e.goldenJSONL("s12_jsonl_s2", res)
	})

	t.Run("keep_going", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "lint", keepGoing)
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "beta \n")
		e.golden("s2_lint_priority_keep_going", res)
	})

	t.Run("keep_going_jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("lint", keepGoing)...)
		e.wantExit(res, 1)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if beta := toolRuns(events, "beta"); len(beta) != 2 || beta[1].Status != "done" {
			t.Errorf("tool_run events of beta = %+v, want a start and a done", beta)
		}
		wantDone(t, events, "lint", 2, 0)
		wantRunDone(t, events, "lint", false, 2, 0, true)
	})

	t.Run("env", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", []string{"DATAMITSU_FAIL_FAST=false"}, "lint")
		e.wantExit(res, 1)
		e.wantMarker("beta", "beta \n")
	})

	t.Run("flag_wins_over_env", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", []string{"DATAMITSU_FAIL_FAST=false"}, "lint", "--fail-fast")
		e.wantExit(res, 1)
		e.wantMarker("beta", "")
	})
}

// TestExecutionFailFastBetweenSubGroups is S3: repository-scoped tasks always
// overlap, so two of them at one priority run one after the other, and a failure
// of the first stops the second, which is reported as not started.
// --fail-fast=false runs it.
func TestExecutionFailFastBetweenSubGroups(t *testing.T) {
	files := map[string]string{"fixture.marker": ""}
	tools := []string{
		clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{}),
		clitest.ShellTool("beta", passScript, clitest.ToolOpSpec{}),
	}

	t.Run("fail_fast", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "lint")
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "")
		e.golden("s3_lint_subgroup_fail_fast", res)
	})

	t.Run("keep_going", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "lint", keepGoing)
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "beta \n")
		e.golden("s3_lint_subgroup_keep_going", res)
	})
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
// its marker in alpha's command nor its cancelled line.
var betaResultRE = regexp.MustCompile(`(?m)^┃ [✓✗] beta\b|─ beta\b`)

// killWindow is how long after a run's start beta would have written its done
// marker had it survived; looking earlier could not tell a kill from a process
// still asleep.
const killWindow = 3500 * time.Millisecond

// TestExecutionFailFastKillsSiblings is S4 (and S12 on it): a failing task
// cancels its running parallel sibling. The sibling is killed, listed as
// cancelled rather than as a result, and its tool_run start is closed by a skip
// event. With --fail-fast=false it finishes. Plan 3 gives each task its own
// invocation ID.
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
		out := e.normalize(res.Stdout)
		if betaResultRE.MatchString(out) {
			t.Errorf("the killed sibling appears in the results:\n%s", out)
		}
		if !strings.Contains(out, "⊘ beta [pkg/b]  cancelled (fail-fast)") {
			t.Errorf("the killed sibling should be listed as cancelled:\n%s", out)
		}
		e.golden("s4_lint_sibling_killed", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e, res := runKilled(t, jsonl("lint")...)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantStopped(t, events, "beta", "pkg/b", "cancelled: fail-fast")
		wantDone(t, events, "lint", 1, 1)
		e.goldenJSONL("s12_jsonl_s4", res)
	})

	runFinished := func(t *testing.T, args ...string) (*execProject, clitest.Result) {
		t.Helper()
		e := newExecProject(t, packagesFiles, packagesSpec, tools...)
		res := e.run("", workers, args...)
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha \n")
		e.wantMarker("beta", "beta started\nbeta done\n")
		return e, res
	}

	t.Run("keep_going", func(t *testing.T) {
		e, res := runFinished(t, "lint", keepGoing)
		e.golden("s4_lint_sibling_keep_going", res)
	})

	t.Run("keep_going_jsonl", func(t *testing.T) {
		_, res := runFinished(t, jsonl("lint", keepGoing)...)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if beta := toolRuns(events, "beta"); len(beta) != 2 || beta[1].Status != "done" || beta[1].Dir != "pkg/b" {
			t.Errorf("tool_run events of beta = %+v, want a start and a done in pkg/b", beta)
		}
		wantDone(t, events, "lint", 2, 0)
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
// starts — no process, no start event — and is reported as not started by a
// single skip event. With --fail-fast=false it runs.
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
		if _, other := onlyFirst(e); !strings.Contains(res.Stdout, "⊘ claim ["+other+"]  not started (fail-fast)") {
			t.Errorf("the cancelled sibling (%s) should be listed as not started:\n%s", other, res.Stdout)
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
		if got := eventsOf(events, func(e clitest.Event) bool { return e.Dir == other && e.Type != "tool_run" }); len(got) != 0 {
			t.Errorf("the sibling in %s never started, yet it has events other than its skip: %+v", other, got)
		}
		wantStopped(t, events, "claim", other, "not started: fail-fast")
		ran := eventsOf(toolRuns(events, "claim"), func(e clitest.Event) bool { return e.Dir == first })
		if len(ran) != 2 || ran[0].Status != "start" || ran[1].Status != "fail" {
			t.Errorf("tool_run events in %s = %+v, want a start and a fail", first, ran)
		}
		wantDone(t, events, "lint", 1, 1)
	})

	t.Run("keep_going", func(t *testing.T) {
		e := newExecProject(t, files, spec, tool)
		res := e.run("", workers, "lint", keepGoing)
		e.wantExit(res, 1)
		switch _, got := e.p.Marker("claim"); got {
		case "claim a first\nclaim b later\n", "claim b first\nclaim a later\n":
		default:
			t.Errorf("claim recorded %q, want a first run in one package and a later one in the other", got)
		}
		e.golden("s4b_lint_sibling_keep_going", res, maskPkgDir)
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
// one task stops at the first failing file. --fail-fast=false runs all three
// files and reports both failures.
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
		// No task was stopped, yet two files were never checked: the run is not
		// complete.
		wantRunDone(t, events, "lint", false, 1, 0, false)
		e.goldenJSONL("s12_jsonl_s5", res)
	})

	t.Run("keep_going", func(t *testing.T) {
		e := newExecProject(t, perFileLoopFiles, fixtureSpec, perFileLoopTool)
		res := e.run("", nil, "lint", keepGoing)
		e.wantExit(res, 1)
		e.wantMarker("alpha", "alpha <TMP>/bad1.txt\nalpha <TMP>/bad2.txt\nalpha <TMP>/ok.txt\n")
		for _, want := range []string{"alpha: bad1.txt failed", "alpha: bad2.txt failed"} {
			if !strings.Contains(res.Stdout, want) {
				t.Errorf("the frame should show %q:\n%s", want, res.Stdout)
			}
		}
		e.golden("s5_lint_per_file_keep_going", res)
	})

	t.Run("keep_going_jsonl", func(t *testing.T) {
		e := newExecProject(t, perFileLoopFiles, fixtureSpec, perFileLoopTool)
		res := e.run("", nil, jsonl("lint", keepGoing)...)
		e.wantExit(res, 1)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantRunDone(t, events, "lint", false, 1, 0, true)
	})
}

// TestExecutionCheckStopsAfterFix is S6: when fix fails, check never starts
// lint — no lint block and no lint phase event — and its closing line says lint
// did not run. With --fail-fast=false lint runs after the failed fix, its footer
// says so, and the run still fails.
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
		if !checkClosingRE.MatchString(res.Stdout) || !strings.Contains(res.Stdout, "· lint not run ·") {
			t.Errorf("check should close with its wall clock and say lint did not run:\n%s", res.Stdout)
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
		wantRunDone(t, events, "check", false, 1, 0, false)
	})

	t.Run("keep_going", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "check", keepGoing)
		e.wantExit(res, 1)
		e.wantMarker("fixer", "fixer \n")
		e.wantMarker("linter", "linter \n")
		if !strings.Contains(res.Stdout, "lint ran after a failed fix") {
			t.Errorf("the lint footer should say it ran after a failed fix:\n%s", res.Stdout)
		}
		e.golden("s6_check_fix_fails_keep_going", res)
	})

	t.Run("keep_going_jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("check", keepGoing)...)
		e.wantExit(res, 1)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		if phases := eventsOf(events, func(e clitest.Event) bool { return e.Type == "phase" }); len(phases) != 2 ||
			phases[0].Op != "fix" || phases[1].Op != "lint" {
			t.Errorf("phase events = %+v, want fix then lint", phases)
		}
		wantRunDone(t, events, "check", false, 2, 0, true)
	})
}

// TestExecutionSetupErrorIsReported: a setup error — here a .datamitsuignore
// line the bundled check rejects — stops the run before any operation, and the
// run is still reported: check's closing line names both operations as not
// run, and the run-level done says the run failed and is incomplete.
func TestExecutionSetupErrorIsReported(t *testing.T) {
	files := map[string]string{"fixture.marker": "", ".datamitsuignore": "no separator here\n"}
	tools := []string{
		clitest.ShellTool("fixer", passScript, clitest.ToolOpSpec{Operation: "fix"}),
		clitest.ShellTool("linter", passScript, clitest.ToolOpSpec{}),
	}

	t.Run("console", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "check")
		e.wantExit(res, 1)
		e.wantMarker("fixer", "")
		if !strings.Contains(res.Stdout, "· fix not run · lint not run ·") || !strings.Contains(res.Stderr, "missing colon separator") {
			t.Errorf("check should report the setup error and close with neither operation run:\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		}
		e.golden("setup_error", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("check")...)
		e.wantExit(res, 1)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantRunDone(t, events, "check", false, 0, 0, false)
	})
}

// checkClosingRE matches check's closing line, durations masked.
var checkClosingRE = regexp.MustCompile(`(?m)^┗━ check · done in \S+ · fix (\S+|not run) · lint (\S+|not run) · setup \S+ ━+$`)

// TestExecutionCheckTotalTime: check closes with the wall clock of the whole
// command, each operation's execution time and the setup around them, and every
// fix, lint and check ends its event stream with a run-level done. Neither
// appears for fix or lint alone on the terminal, nor under --explain.
func TestExecutionCheckTotalTime(t *testing.T) {
	files := map[string]string{"fixture.marker": ""}
	tools := []string{
		clitest.ShellTool("fixer", passScript, clitest.ToolOpSpec{Operation: "fix"}),
		clitest.ShellTool("linter", passScript, clitest.ToolOpSpec{}),
	}

	t.Run("console", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, "check")
		e.wantExit(res, 0)
		if !checkClosingRE.MatchString(res.Stdout) || strings.Contains(res.Stdout, "not run") {
			t.Errorf("check should close with its wall clock and both operations' times:\n%s", res.Stdout)
		}
		e.golden("check_total_time", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		e := newExecProject(t, files, fixtureSpec, tools...)
		res := e.run("", nil, jsonl("check")...)
		e.wantExit(res, 0)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantRunDone(t, events, "check", true, 2, 0, true)
		var operations int64
		for _, d := range eventsOf(events, operationDone) {
			ms, err := d.Fields["duration_ms"].(json.Number).Int64()
			if err != nil {
				t.Fatalf("done %s carries no duration: %+v", d.Op, d.Fields)
			}
			operations += ms
		}
		total, err := runDone(t, events).Fields["duration_ms"].(json.Number).Int64()
		if err != nil || total < operations {
			t.Errorf("run-level duration_ms = %d, want at least the operations' %d", total, operations)
		}
		e.goldenJSONL("s12_jsonl_check_total_time", res)
	})

	for _, op := range []string{"fix", "lint"} {
		t.Run(op+"_alone", func(t *testing.T) {
			e := newExecProject(t, files, fixtureSpec, tools...)
			res := e.run("", nil, op)
			e.wantExit(res, 0)
			if strings.Contains(res.Stdout, "┗━ check") || strings.Contains(res.Stdout, "setup") {
				t.Errorf("%s alone printed check's closing line:\n%s", op, res.Stdout)
			}
			events := e.run("", nil, jsonl(op)...)
			e.wantExit(events, 0)
			parsed := clitest.MustParseJSONL(t, events.Stderr)
			clitest.AssertChains(t, parsed)
			wantRunDone(t, parsed, op, true, 1, 0, true)
		})
	}

	for _, mode := range []string{"summary", "detailed", "json"} {
		t.Run("explain_"+mode, func(t *testing.T) {
			e := newExecProject(t, files, fixtureSpec, tools...)
			res := e.run("", nil, jsonl("check", "--explain="+mode)...)
			e.wantExit(res, 0)
			if strings.Contains(res.Stdout, "┗━ check") {
				t.Errorf("check --explain=%s printed the closing line:\n%s", mode, res.Stdout)
			}
			if done := eventsOf(clitest.MustParseJSONL(t, res.Stderr), func(e clitest.Event) bool { return e.Type == "done" }); len(done) != 0 {
				t.Errorf("check --explain=%s emitted done events: %+v", mode, done)
			}
		})
	}
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

// nativeSkipped declares a binary app built only for another OS, so the host
// skips its tool: the platform skip --fail-on-skip reports.
func nativeSkipped() string {
	otherOS := "windows"
	if runtime.GOOS == otherOS {
		otherOS = "linux"
	}
	return fmt.Sprintf(`c.apps["native"] = { binary: { binaries: { %s: { amd64: { unknown: {
  url: "https://example.invalid/native", contentType: "raw",
  hash: "3f79bb7b435b05321651daefd374cdc681dc06faa65e374e38337b88ca046dea" } } } } } };
c.tools["native"] = { name: "native", operations: { lint: { app: "native", args: [], scope: "repository" } } };
`, otherOS)
}

// TestExecutionFailOnSkip is S10: --fail-on-skip turns a tool whose binary has
// no build for this host into exit 4, the code of a run that did not look at
// everything. A tool failure still exits 1, and a run that fails both
// --fail-on-skip and --require-coverage prints both and exits 4 once.
func TestExecutionFailOnSkip(t *testing.T) {
	spec := clitest.ShellConfigSpec{ProjectTypes: fixtureTypes, Extra: nativeSkipped()}

	t.Run("skip", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": ""}, spec,
			clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
		res := e.run("", nil, "lint", "--fail-on-skip")
		e.wantExit(res, 4)
		e.wantMarker("alpha", "alpha \n")
		if !strings.Contains(res.Stdout, "⊘ native") {
			t.Errorf("the skipped tool should be listed:\n%s", res.Stdout)
		}
		e.golden("s10_lint_fail_on_skip", res, maskHost)
	})

	t.Run("tool_failure_wins", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": ""}, spec,
			clitest.ShellTool("alpha", failScript, clitest.ToolOpSpec{}))
		res := e.run("", nil, "lint", "--fail-on-skip")
		e.wantExit(res, 1)
		if strings.Contains(res.Stderr, "--fail-on-skip") {
			t.Errorf("a tool failure should be the reported outcome:\n%s", res.Stderr)
		}
	})

	t.Run("with_require_coverage", func(t *testing.T) {
		e := newExecProject(t, map[string]string{"fixture.marker": "", "sub/file.txt": "x\n"}, spec,
			clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{Scope: "per-project"}))
		res := e.run("sub", nil, "lint", "--fail-on-skip", "--require-coverage=repo")
		e.wantExit(res, 4)
		skip, cov := strings.Index(res.Stderr, "--fail-on-skip:"), strings.Index(res.Stderr, "--require-coverage=repo:")
		if skip < 0 || cov < skip {
			t.Errorf("stderr should report --fail-on-skip, then --require-coverage:\n%s", res.Stderr)
		}
		e.golden("s10_lint_fail_on_skip_and_require_coverage", res, maskHost)
	})
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

// TestExecutionUsageErrors is S13: a malformed invocation — an unknown flag, a
// flag value or DATAMITSU_* value it does not accept, a combination it refuses
// before running — exits 2 with an error line, apart from the 1 of a failed
// tool.
func TestExecutionUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		args []string
	}{
		{"bogus_flag", nil, []string{"lint", "--bogus-flag"}},
		{"widen_to_invalid", nil, []string{"lint", "--widen-to=Repo"}},
		{"require_coverage_with_tools", nil, []string{"lint", "--require-coverage=unit", "--tools", "alpha"}},
		{"fail_fast_env_invalid", []string{"DATAMITSU_FAIL_FAST=yes"}, []string{"lint"}},
		{"fail_fast_flag_invalid", nil, []string{"lint", "--fail-fast=maybe"}},
		{"explain_invalid", nil, []string{"lint", "--explain=bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
				clitest.ShellTool("alpha", passScript, clitest.ToolOpSpec{}))
			res := e.run("", tc.env, tc.args...)
			e.wantExit(res, 2)
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

// TestExecutionInterrupted: SIGINT stops a run the way fail-fast does, and is
// reported as its own cause. The running tool is killed and listed as
// cancelled, the tool of a later priority as not started, and the run exits
// 130, the status a shell reported when the signal simply killed datamitsu.
func TestExecutionInterrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGINT cannot be sent to a process on Windows; the interrupted-run report is left unverified")
	}
	files := map[string]string{"fixture.marker": ""}
	tools := []string{
		clitest.ShellTool("sleeper", `echo "$0 started" >> "$MARKERS/$0"; sleep 3; echo "$0 done" >> "$MARKERS/$0"`,
			clitest.ToolOpSpec{Priority: 10}),
		clitest.ShellTool("later", passScript, clitest.ToolOpSpec{Priority: 20}),
	}

	interrupt := func(t *testing.T, args ...string) (*execProject, clitest.Result) {
		t.Helper()
		e := newExecProject(t, files, fixtureSpec, tools...)
		start := time.Now()
		proc := clitest.Start(t, clitest.RunOptions{Dir: e.p.Dir, CacheDir: e.cache},
			append([]string{"--no-auto-config", "--config", e.cfg}, args...)...)
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if ran, _ := e.p.Marker("sleeper"); ran {
				break
			}
			if time.Now().After(deadline) {
				_ = proc.Signal(syscall.SIGKILL)
				t.Fatalf("sleeper never started:\n%+v", proc.Wait())
			}
		}
		if err := proc.Signal(syscall.SIGINT); err != nil {
			t.Fatalf("send SIGINT: %v", err)
		}
		res := proc.Wait()
		time.Sleep(time.Until(start.Add(killWindow)))
		e.wantExit(res, 130)
		e.wantMarker("sleeper", "sleeper started\n")
		e.wantMarker("later", "")
		return e, res
	}

	t.Run("console", func(t *testing.T) {
		e, res := interrupt(t, "lint")
		for _, want := range []string{"⊘ sleeper  cancelled (interrupted)", "⊘ later    not started (interrupted)"} {
			if !strings.Contains(res.Stdout, want) {
				t.Errorf("stdout should list %q:\n%s", want, res.Stdout)
			}
		}
		e.golden("s4_lint_interrupted", res)
	})

	t.Run("jsonl", func(t *testing.T) {
		_, res := interrupt(t, jsonl("lint")...)
		events := clitest.MustParseJSONL(t, res.Stderr)
		clitest.AssertChains(t, events)
		wantStopped(t, events, "sleeper", "", "cancelled: interrupted")
		wantStopped(t, events, "later", "", "not started: interrupted")
		wantDone(t, events, "lint", 0, 2)
		wantRunDone(t, events, "lint", false, 0, 2, false)
		errs := eventsOf(events, func(e clitest.Event) bool { return e.Type == "error" && e.Tool == "" })
		if len(errs) != 1 || errs[0].Msg != "interrupted by SIGINT" {
			t.Errorf("run error events = %+v, want one saying the run was interrupted by SIGINT", errs)
		}
	})
}
