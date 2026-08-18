package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. The runner prints summaries straight to stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestPrintOperationFooterShowsSkipped(t *testing.T) {
	out := captureStdout(t, func() { printOperationFooter(nil, 0, 0, 0, 2) })
	if !strings.Contains(out, "2 skipped") {
		t.Errorf("footer missing skipped count: %q", out)
	}
}

func TestPrintSkippedTools(t *testing.T) {
	skipped := []tooling.SkippedTool{
		{ToolName: "trufflehog", Reason: tooling.SkipReasonConfig, Detail: "runs in CI only"},
		{ToolName: "typstyle", Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"},
	}
	out := captureStdout(t, func() { printSkippedTools(skipped, 12) })
	for _, want := range []string{"⊘", "trufflehog", "runs in CI only", "typstyle", "no binary for linux/arm64/musl"} {
		if !strings.Contains(out, want) {
			t.Errorf("printSkippedTools output missing %q: %s", want, out)
		}
	}
}

func TestRecordSkipsOnlyPlatform(t *testing.T) {
	sc := &sharedContext{platformSkipped: map[string]struct{}{}}
	sc.recordSkips([]tooling.SkippedTool{
		{ToolName: "trufflehog", Reason: tooling.SkipReasonConfig},
		{ToolName: "typstyle", Reason: tooling.SkipReasonUnsupportedPlatform},
		{ToolName: "syncpack", Reason: tooling.SkipReasonNotNarrowable},
	})
	if _, ok := sc.platformSkipped["typstyle"]; !ok {
		t.Error("platform skip should be recorded")
	}
	if _, ok := sc.platformSkipped["trufflehog"]; ok {
		t.Error("config skip must not be recorded (it is intentional)")
	}
	// A tool that could not be narrowed leaves the answer incomplete...
	if _, ok := sc.narrowed["syncpack"]; !ok {
		t.Error("not-narrowable should count against coverage")
	}
	// ...while one the repository deliberately disabled does not.
	if _, ok := sc.narrowed["trufflehog"]; ok {
		t.Error("a config skip is an answer, not an incomplete one")
	}
}

func TestSkipFailure(t *testing.T) {
	// failOnSkip off: never errors, even with platform skips.
	sc := &sharedContext{failOnSkip: false, platformSkipped: map[string]struct{}{"typstyle": {}}}
	if err := sc.skipFailure(); err != nil {
		t.Errorf("failOnSkip off should not error: %v", err)
	}
	// failOnSkip on, only config skips (none recorded): no error.
	sc = &sharedContext{failOnSkip: true, platformSkipped: map[string]struct{}{}}
	if err := sc.skipFailure(); err != nil {
		t.Errorf("no platform skips should not error: %v", err)
	}
	// failOnSkip on with a platform skip: error names the tool.
	sc = &sharedContext{failOnSkip: true, platformSkipped: map[string]struct{}{"typstyle": {}}}
	err := sc.skipFailure()
	if err == nil || !strings.Contains(err.Error(), "typstyle") {
		t.Errorf("expected error naming typstyle, got %v", err)
	}
}

func TestRunSingleOperationRendersAndRecordsSkips(t *testing.T) {
	t.Setenv("CI", "true") // deterministic Plain-mode display

	var order []string
	plan := &tooling.ExecutionPlan{
		Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{
			{ToolName: "shellcheck", OpConfig: config.ToolOperation{App: "shellcheck", Scope: config.ToolScopeRepository}},
		}}},
		Skipped: []tooling.SkippedTool{
			{ToolName: "trufflehog", Operation: config.OpLint, Reason: tooling.SkipReasonConfig, Detail: "runs in CI only"},
			{ToolName: "typstyle", Operation: config.OpLint, Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"},
		},
	}
	sc := &sharedContext{
		planner:         &fakePlanner{plan: plan},
		executor:        &fakeExecutor{order: &order},
		binMgr:          &fakeEnsurer{order: &order},
		timings:         timing.New(),
		platformSkipped: map[string]struct{}{},
		nameWidth:       10,
	}

	out := captureStdout(t, func() {
		if err := runSingleOperation(context.Background(), sc, config.OpLint); err != nil {
			t.Fatalf("runSingleOperation: %v", err)
		}
	})

	if !strings.Contains(out, "trufflehog") || !strings.Contains(out, "typstyle") {
		t.Errorf("skip lines missing from output: %s", out)
	}
	if !strings.Contains(out, "2 skipped") {
		t.Errorf("footer skipped count missing: %s", out)
	}
	if _, ok := sc.platformSkipped["typstyle"]; !ok {
		t.Error("platform skip not recorded for --fail-on-skip")
	}
	if _, ok := sc.platformSkipped["trufflehog"]; ok {
		t.Error("config skip should not be recorded for --fail-on-skip")
	}
}

// When everything is skipped (no runnable groups), the skip-only block still
// renders and platform skips are still recorded.
func TestRunSingleOperationAllSkipped(t *testing.T) {
	t.Setenv("CI", "true")

	var order []string
	plan := &tooling.ExecutionPlan{
		Skipped: []tooling.SkippedTool{
			{ToolName: "typstyle", Operation: config.OpLint, Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"},
		},
	}
	sc := &sharedContext{
		planner:         &fakePlanner{plan: plan},
		executor:        &fakeExecutor{order: &order},
		binMgr:          &fakeEnsurer{order: &order},
		timings:         timing.New(),
		platformSkipped: map[string]struct{}{},
		nameWidth:       10,
	}

	out := captureStdout(t, func() {
		if err := runSingleOperation(context.Background(), sc, config.OpLint); err != nil {
			t.Fatalf("runSingleOperation: %v", err)
		}
	})

	if !strings.Contains(out, "typstyle") || !strings.Contains(out, "1 skipped") {
		t.Errorf("all-skipped block missing skip details: %s", out)
	}
	if _, ok := sc.platformSkipped["typstyle"]; !ok {
		t.Error("platform skip not recorded in all-skipped path")
	}
}

func TestCoverageFailure(t *testing.T) {
	narrowed := func(names ...string) map[string]struct{} {
		m := map[string]struct{}{}
		for _, n := range names {
			m[n] = struct{}{}
		}
		return m
	}

	t.Run("no flag never fails", func(t *testing.T) {
		sc := &sharedContext{narrowed: narrowed("syncpack")}
		if err := sc.coverageFailure(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("unit fails on an incomplete answer", func(t *testing.T) {
		sc := &sharedContext{opts: Options{RequireCoverage: "unit"}, narrowed: narrowed("syncpack")}
		if err := sc.coverageFailure(); err == nil {
			t.Error("expected a failure")
		}
	})

	t.Run("unit passes a complete one", func(t *testing.T) {
		sc := &sharedContext{opts: Options{RequireCoverage: "unit"}, narrowed: narrowed()}
		if err := sc.coverageFailure(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	// Without the selection clause this would pass having checked one unit of
	// nine: per-task coverage only speaks for the units that ran.
	t.Run("repo requires the whole repository as the target", func(t *testing.T) {
		sc := &sharedContext{
			opts:      Options{RequireCoverage: "repo"},
			narrowed:  narrowed(),
			selection: tooling.Selection{Mode: tooling.SelectionPaths},
		}
		if err := sc.coverageFailure(); err == nil {
			t.Error("a narrowed selection cannot satisfy repo coverage")
		}
	})

	t.Run("repo passes on a full run", func(t *testing.T) {
		sc := &sharedContext{
			opts:      Options{RequireCoverage: "repo"},
			narrowed:  narrowed(),
			selection: tooling.Selection{Mode: tooling.SelectionAll},
		}
		if err := sc.coverageFailure(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	// A coverage failure must be distinguishable from a tool failure, or a
	// pipeline cannot act on either.
	t.Run("carries its own exit code", func(t *testing.T) {
		sc := &sharedContext{opts: Options{RequireCoverage: "unit"}, narrowed: narrowed("syncpack")}
		err := sc.coverageFailure()
		var coded interface{ ExitCode() int }
		if !errors.As(err, &coded) {
			t.Fatalf("expected a coded error, got: %v", err)
		}
		if coded.ExitCode() != ExitCoverage {
			t.Errorf("exit code = %d, want %d", coded.ExitCode(), ExitCoverage)
		}
	})
}
