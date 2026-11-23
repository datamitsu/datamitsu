package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	zerovalue_time "time"

	zerovalue_cienv "github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/exitcode"
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
	out := captureStdout(t, func() { printOperationFooter(nil, 0, 0, 0, 2, 0, "", 0, 0) })
	if !strings.Contains(out, "2 skipped") {
		t.Errorf("footer missing skipped count: %q", out)
	}
}

func TestPrintSkippedTools(t *testing.T) {
	skipped := []tooling.SkippedTool{
		{Operation: "", ToolName: "trufflehog", Reason: tooling.SkipReasonConfig, Detail: "runs in CI only"},
		{Operation: "", ToolName: "typstyle", Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"},
	}
	out := captureStdout(t, func() { printSkippedTools(skipped, 12) })
	for _, want := range []string{"⊘", "trufflehog", "runs in CI only", "typstyle", "no binary for linux/arm64/musl"} {
		if !strings.Contains(out, want) {
			t.Errorf("printSkippedTools output missing %q: %s", want, out)
		}
	}
}

func TestRecordSkipsOnlyPlatform(t *testing.T) {
	sc := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, platformSkipped: map[string]struct{}{},
	}
	sc.recordSkips([]tooling.SkippedTool{
		{Operation: "", Detail: "", ToolName: "trufflehog", Reason: tooling.SkipReasonConfig},
		{Operation: "", Detail: "", ToolName: "typstyle", Reason: tooling.SkipReasonUnsupportedPlatform},
		{Operation: "", Detail: "", ToolName: "syncpack", Reason: tooling.SkipReasonNotNarrowable},
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
	sc := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, failOnSkip: false, platformSkipped: map[string]struct{}{"typstyle": {}},
	}
	if err := sc.skipFailure(); err != nil {
		t.Errorf("failOnSkip off should not error: %v", err)
	}
	// failOnSkip on, only config skips (none recorded): no error.
	sc = &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, failOnSkip: true, platformSkipped: map[string]struct{}{},
	}
	if err := sc.skipFailure(); err != nil {
		t.Errorf("no platform skips should not error: %v", err)
	}
	// failOnSkip on with a platform skip: error names the tool.
	sc = &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, failOnSkip: true, platformSkipped: map[string]struct{}{"typstyle": {}},
	}
	err := sc.skipFailure()
	if err == nil || !strings.Contains(err.Error(), "typstyle") {
		t.Errorf("expected error naming typstyle, got %v", err)
	}
}

func TestRunSingleOperationRendersAndRecordsSkips(t *testing.T) {
	t.Setenv("CI", "true") // deterministic Plain-mode display

	var order []string
	plan := &tooling.ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Groups: []tooling.TaskGroup{{
			Priority: 0, Tasks: []tooling.Task{
				{ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "shellcheck", OpConfig: config.ToolOperation{Args: nil, Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "shellcheck", Scope: config.ToolScopeRepository}},
			},
		}},
		Skipped: []tooling.SkippedTool{
			{ToolName: "trufflehog", Operation: config.OpLint, Reason: tooling.SkipReasonConfig, Detail: "runs in CI only"},
			{ToolName: "typstyle", Operation: config.OpLint, Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"},
		},
	}
	sc := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,

		cfg:             nil,
		rootPath:        "",
		cwdPath:         "",
		selection:       tooling.Selection{Mode: 0, Dir: "", Paths: nil},
		opts:            Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil},
		files:           nil,
		selectedTools:   nil,
		explainLevel:    "",
		projectCache:    nil,
		parserMgr:       nil,
		parseProblems:   nil,
		ignoredFailOn:   nil,
		failOnSkip:      false,
		narrowed:        nil,
		failFast:        false,
		afterFailedFix:  false,
		summaries:       nil,
		fileScoped:      false,
		report:          nil,
		secrets:         nil,
		annotator:       nil,
		baselineMatcher: nil,
		placement:       nil, startedAt: zerovalue_time.Time{},
		ci:          zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		ciRuntime:   zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""},
		annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false},
		planner: &fakePlanner{
			planFn: nil,
			calls:  0,
			plan:   plan,
		},
		executor:        &fakeExecutor{called: false, results: nil, onResult: nil, order: &order},
		binMgr:          &fakeEnsurer{names: nil, called: false, err: nil, order: &order},
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
		ConfigName: "",
		FailOn:     "",
		Groups:     nil,
		Skipped: []tooling.SkippedTool{
			{ToolName: "typstyle", Operation: config.OpLint, Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "linux/arm64/musl"},
		},
	}
	sc := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,

		cfg:             nil,
		rootPath:        "",
		cwdPath:         "",
		selection:       tooling.Selection{Mode: 0, Dir: "", Paths: nil},
		opts:            Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil},
		files:           nil,
		selectedTools:   nil,
		explainLevel:    "",
		projectCache:    nil,
		parserMgr:       nil,
		parseProblems:   nil,
		ignoredFailOn:   nil,
		failOnSkip:      false,
		narrowed:        nil,
		failFast:        false,
		afterFailedFix:  false,
		summaries:       nil,
		fileScoped:      false,
		report:          nil,
		secrets:         nil,
		annotator:       nil,
		baselineMatcher: nil,
		placement:       nil, startedAt: zerovalue_time.Time{},
		ci:          zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		ciRuntime:   zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""},
		annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false},
		planner: &fakePlanner{
			planFn: nil,
			calls:  0,
			plan:   plan,
		},
		executor:        &fakeExecutor{called: false, results: nil, onResult: nil, order: &order},
		binMgr:          &fakeEnsurer{names: nil, called: false, err: nil, order: &order},
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
		sc := &sharedContext{
			preparedTools:        false,
			parserPrewarmed:      false,
			bundledFixFindings:   nil,
			bundledFixChanges:    nil,
			bundledLintFindings:  nil,
			bundledLintErr:       nil,
			completedBundledLint: false,
			cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, narrowed: narrowed("syncpack"),
		}
		if err := sc.coverageFailure(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("unit fails on an incomplete answer", func(t *testing.T) {
		sc := &sharedContext{
			preparedTools:        false,
			parserPrewarmed:      false,
			bundledFixFindings:   nil,
			bundledFixChanges:    nil,
			bundledLintFindings:  nil,
			bundledLintErr:       nil,
			completedBundledLint: false,
			cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, opts: Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "unit"}, narrowed: narrowed("syncpack"),
		}
		if err := sc.coverageFailure(); err == nil {
			t.Error("expected a failure")
		}
	})

	t.Run("unit passes a complete one", func(t *testing.T) {
		sc := &sharedContext{
			preparedTools:        false,
			parserPrewarmed:      false,
			bundledFixFindings:   nil,
			bundledFixChanges:    nil,
			bundledLintFindings:  nil,
			bundledLintErr:       nil,
			completedBundledLint: false,
			cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, opts: Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "unit"}, narrowed: narrowed(),
		}
		if err := sc.coverageFailure(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	// Without the selection clause this would pass having checked one unit of
	// nine: per-task coverage only speaks for the units that ran.
	t.Run("repo requires the whole repository as the target", func(t *testing.T) {
		sc := &sharedContext{
			preparedTools:        false,
			parserPrewarmed:      false,
			bundledFixFindings:   nil,
			bundledFixChanges:    nil,
			bundledLintFindings:  nil,
			bundledLintErr:       nil,
			completedBundledLint: false,

			cfg:             nil,
			rootPath:        "",
			cwdPath:         "",
			files:           nil,
			selectedTools:   nil,
			explainLevel:    "",
			planner:         nil,
			projectCache:    nil,
			executor:        nil,
			binMgr:          nil,
			timings:         nil,
			parserMgr:       nil,
			parseProblems:   nil,
			ignoredFailOn:   nil,
			nameWidth:       0,
			failOnSkip:      false,
			platformSkipped: nil,
			failFast:        false,
			afterFailedFix:  false,
			summaries:       nil,
			fileScoped:      false,
			report:          nil,
			secrets:         nil,
			annotator:       nil,
			baselineMatcher: nil,
			placement:       nil, startedAt: zerovalue_time.Time{},
			ci:          zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
			ciRuntime:   zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""},
			annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false},
			opts:        Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "repo"},
			narrowed:    narrowed(),
			selection:   tooling.Selection{Dir: "", Paths: nil, Mode: tooling.SelectionPaths},
		}
		if err := sc.coverageFailure(); err == nil {
			t.Error("a narrowed selection cannot satisfy repo coverage")
		}
	})

	t.Run("repo passes on a full run", func(t *testing.T) {
		sc := &sharedContext{
			preparedTools:        false,
			parserPrewarmed:      false,
			bundledFixFindings:   nil,
			bundledFixChanges:    nil,
			bundledLintFindings:  nil,
			bundledLintErr:       nil,
			completedBundledLint: false,

			cfg:             nil,
			rootPath:        "",
			cwdPath:         "",
			files:           nil,
			selectedTools:   nil,
			explainLevel:    "",
			planner:         nil,
			projectCache:    nil,
			executor:        nil,
			binMgr:          nil,
			timings:         nil,
			parserMgr:       nil,
			parseProblems:   nil,
			ignoredFailOn:   nil,
			nameWidth:       0,
			failOnSkip:      false,
			platformSkipped: nil,
			failFast:        false,
			afterFailedFix:  false,
			summaries:       nil,
			fileScoped:      false,
			report:          nil,
			secrets:         nil,
			annotator:       nil,
			baselineMatcher: nil,
			placement:       nil, startedAt: zerovalue_time.Time{},
			ci:          zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
			ciRuntime:   zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""},
			annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false},
			opts:        Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "repo"},
			narrowed:    narrowed(),
			selection:   tooling.Selection{Dir: "", Paths: nil, Mode: tooling.SelectionAll},
		}
		if err := sc.coverageFailure(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	// A coverage failure must be distinguishable from a tool failure, or a
	// pipeline cannot act on either.
	t.Run("carries its own exit code", func(t *testing.T) {
		sc := &sharedContext{
			preparedTools:        false,
			parserPrewarmed:      false,
			bundledFixFindings:   nil,
			bundledFixChanges:    nil,
			bundledLintFindings:  nil,
			bundledLintErr:       nil,
			completedBundledLint: false,
			cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, opts: Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "unit"}, narrowed: narrowed("syncpack"),
		}
		err := sc.coverageFailure()
		var coded interface{ ExitCode() int }
		if !errors.As(err, &coded) {
			t.Fatalf("expected a coded error, got: %v", err)
		}
		if coded.ExitCode() != exitcode.Coverage {
			t.Errorf("exit code = %d, want %d", coded.ExitCode(), exitcode.Coverage)
		}
	})
}
