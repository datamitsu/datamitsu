package runner

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	zerovalue_time "time"

	zerovalue_cienv "github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// Rank() reads an unvalidated string through a map, so an unrecognised value
// ranks 0 — the same as the strictest level. Unchecked, --widen-to=Repo asks for
// the widest policy and silently gets the narrowest.
func TestOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{"empty", Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, ""},
		{"widen target", Options{RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, WidenTo: "target"}, ""},
		{"widen unit", Options{RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, WidenTo: "unit"}, ""},
		{"widen repo", Options{RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, WidenTo: "repo"}, ""},
		{"widen wrong case", Options{RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, WidenTo: "Repo"}, "invalid --widen-to"},
		{"widen garbage", Options{RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, WidenTo: "everything"}, "invalid --widen-to"},
		{"require unit", Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "unit"}, ""},
		{"require repo", Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "repo"}, ""},
		// "target" asserts only what was named, which is always true.
		{"require target", Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "target"}, "always true"},
		{"require garbage", Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "everything"}, "invalid --require-coverage"},
		{"require wrong case", Options{WidenTo: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil, RequireCoverage: "Unit"}, "invalid --require-coverage"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() = nil, want an error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validate() = %v, want it to mention %q", err, tt.wantErr)
			}
			if !isUsageError(err) {
				t.Errorf("validate() = %v, want a usage error (exit %d)", err, exitcode.Usage)
			}
		})
	}
}

func isUsageError(err error) bool {
	coded, ok := errors.AsType[interface {
		error
		ExitCode() int
	}](err)
	return ok && coded.ExitCode() == exitcode.Usage
}

// --require-coverage with --tools is refused before anything runs: the caller
// asked for an assertion the run cannot make, a usage error.
func TestRequireCoverageWithToolsIsAUsageError(t *testing.T) {
	if !isUsageError(errRequireCoverageWithTools) {
		t.Errorf("errRequireCoverageWithTools = %v, want a usage error (exit %d)", errRequireCoverageWithTools, exitcode.Usage)
	}
}

// --require-coverage must mean the same thing with and without --explain, or it
// is useless as a CI gate: the cheap plan-only check would pass exactly where
// the real run fails. Only the reasons differ from --fail-on-skip, which stays
// inert in explain mode.
func TestExplainAndExecutingPathsAgreeOnCoverage(t *testing.T) {
	skips := []tooling.SkippedTool{
		{Operation: "", Detail: "", ToolName: "syncpack", Reason: tooling.SkipReasonNotNarrowable},
		{Operation: "", Detail: "", ToolName: "typstyle", Reason: tooling.SkipReasonUnsupportedPlatform},
		{Operation: "", Detail: "", ToolName: "bearer", Reason: tooling.SkipReasonConfig},
	}

	explain := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, platformSkipped: map[string]struct{}{},
	}
	explain.recordNarrowingSkips(skips)

	executing := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, platformSkipped: map[string]struct{}{},
	}
	executing.recordSkips(skips)

	if len(explain.narrowed) != len(executing.narrowed) {
		t.Fatalf("explain recorded %v, executing recorded %v", explain.narrowed, executing.narrowed)
	}
	for name := range executing.narrowed {
		if _, ok := explain.narrowed[name]; !ok {
			t.Errorf("%q counts against coverage when running but not when explaining", name)
		}
	}
	if _, ok := explain.narrowed["bearer"]; ok {
		t.Error("a config skip counted against coverage; opting a tool out is an answer, not a gap")
	}
	// --fail-on-skip is about a host lacking a binary, which only matters if
	// something was going to run.
	if len(explain.platformSkipped) != 0 {
		t.Errorf("explain armed --fail-on-skip with %v", explain.platformSkipped)
	}
}

func TestRecordCoverage(t *testing.T) {
	sc := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, narrowed: map[string]struct{}{},
	}
	sc.recordCoverage(&tooling.ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Skipped:    nil, Groups: []tooling.TaskGroup{{
			Priority: 0, Tasks: []tooling.Task{
				{ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, ToolName: "tsc", Coverage: tooling.CoveragePartial},
				{ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, ToolName: "eslint", Coverage: tooling.CoverageComplete},
			},
		}},
	})

	if _, ok := sc.narrowed["tsc"]; !ok {
		t.Error("a partial task must count against coverage")
	}
	if _, ok := sc.narrowed["eslint"]; ok {
		t.Error("a complete task must not count against coverage")
	}
}

// A green result on a narrowed run must not read as a green repository.
func TestTargetLine(t *testing.T) {
	root := filepath.FromSlash("/repo")
	paths := func(names ...string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = filepath.Join(root, filepath.FromSlash(n))
		}
		return out
	}

	tests := []struct {
		name string
		sel  tooling.Selection
		want string // "" means no line at all
	}{
		{"whole repository is the common case", tooling.Selection{Dir: "", Paths: nil, Mode: tooling.SelectionAll}, ""},
		{"nothing staged", tooling.Selection{Dir: "", Paths: nil, Mode: tooling.SelectionEmpty}, "nothing staged"},
		{
			"one file is singular",
			tooling.Selection{Dir: "", Mode: tooling.SelectionPaths, Paths: paths("a.ts")},
			"1 file · a.ts",
		},
		{
			"several files are plural",
			tooling.Selection{Dir: "", Mode: tooling.SelectionPaths, Paths: paths("a.ts", "b.ts")},
			"2 files · a.ts b.ts",
		},
		{
			"a long list is truncated",
			tooling.Selection{Dir: "", Mode: tooling.SelectionPaths, Paths: paths("a.ts", "b.ts", "c.ts", "d.ts", "e.ts")},
			"+2 more",
		},
		{
			"a subtree names the directory",
			tooling.Selection{Paths: nil, Mode: tooling.SelectionSubtree, Dir: filepath.Join(root, "pkg")},
			"target: pkg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &sharedContext{
				preparedTools:        false,
				parserPrewarmed:      false,
				bundledFixFindings:   nil,
				bundledFixChanges:    nil,
				bundledLintFindings:  nil,
				bundledLintErr:       nil,
				completedBundledLint: false,
				cfg:                  nil, cwdPath: "", opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, rootPath: root, selection: tt.sel,
			}
			got := sc.targetLine()

			if tt.want == "" {
				if got != "" {
					t.Fatalf("targetLine() = %q, want no annotation", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("targetLine() = %q, want it to contain %q", got, tt.want)
			}
			if !strings.Contains(got, "narrowed run") {
				t.Errorf("targetLine() = %q, want it to say the run was narrowed", got)
			}
		})
	}
}

// Paths are shown relative to the git root so the line stays readable and does
// not change with where the repository lives.
func TestRelativeToRoot(t *testing.T) {
	root := filepath.FromSlash("/repo")
	got := relativeToRoot([]string{filepath.Join(root, "pkg", "a.ts")}, root)

	if len(got) != 1 || got[0] != filepath.FromSlash("pkg/a.ts") {
		t.Errorf("relativeToRoot = %v, want [pkg/a.ts]", got)
	}
}

func TestValidWidenTo(t *testing.T) {
	for _, w := range []config.WidenTo{config.WidenToTarget, config.WidenToUnit, config.WidenToRepo} {
		if !config.ValidWidenTo(w) {
			t.Errorf("ValidWidenTo(%q) = false, want true", w)
		}
	}
	for _, w := range []config.WidenTo{"", "Repo", "everything"} {
		if config.ValidWidenTo(w) {
			t.Errorf("ValidWidenTo(%q) = true, want false", w)
		}
	}
}
