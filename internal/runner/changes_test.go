package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	zerovalue_cienv "github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/gitutil"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// TestStepCallbackStopsObservingOnAFailedSnapshot: a snapshot that cannot be
// taken — first or later — leaves the fix's changes unobserved from there on,
// with the reason; steps are still recorded. lint observes nothing.
func TestStepCallbackStopsObservingOnAFailedSnapshot(t *testing.T) {
	task := tooling.Task{Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ID: "fmt::1", ToolName: "fmt", Operation: config.OpFix}
	plan := &tooling.ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []tooling.TaskGroup{{Priority: 0, Tasks: []tooling.Task{task}}}}
	tests := []struct {
		name      string
		failAt    int
		observed  bool
		reason    string
		wantSteps int
	}{
		{reason: "", name: "every snapshot taken", failAt: -1, observed: true, wantSteps: 1},
		{observed: false, name: "the first fails", failAt: 0, reason: report.ChangesSnapshotFailed, wantSteps: 1},
		{observed: false, name: "a later one fails", failAt: 1, reason: report.ChangesSnapshotFailed, wantSteps: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			prev := takeSnapshot
			takeSnapshot = func(context.Context, string, []string) (gitutil.Snapshot, error) {
				defer func() { calls++ }()
				if calls == tt.failAt {
					return gitutil.Snapshot{}, errors.New("status failed")
				}
				return gitutil.Snapshot{}, nil
			}
			t.Cleanup(func() { takeSnapshot = prev })

			acc := report.NewAccumulator(report.Options{Root: "", Tools: nil, Apps: nil, FailOn: "", Parsers: nil, Secrets: nil})
			rec := acc.BeginOperation(string(config.OpFix), plan, nil)
			sc := &sharedContext{
				preparedTools:        false,
				parserPrewarmed:      false,
				bundledFixFindings:   nil,
				bundledFixChanges:    nil,
				bundledLintFindings:  nil,
				bundledLintErr:       nil,
				completedBundledLint: false,
				cfg:                  nil, cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, rootPath: t.TempDir(),
			}
			cb := sc.stepCallback(context.Background(), config.OpFix, rec)
			cb(1, plan.Groups[0].Tasks)
			op := rec.Operation()
			if op.ChangesObserved != tt.observed || op.ChangesReason != tt.reason {
				t.Errorf("observed %v, reason %q; want %v, %q", op.ChangesObserved, op.ChangesReason, tt.observed, tt.reason)
			}
			if tt.reason != "" && op.ChangesDetail != "status failed" {
				t.Errorf("detail = %q, want the error", op.ChangesDetail)
			}
			if sc.stepCallback(context.Background(), config.OpLint, rec) != nil {
				t.Error("lint should take no snapshot")
			}
		})
	}
}

func TestAgentChanges(t *testing.T) {
	changed := report.Operation{
		Success:       false,
		Duration:      0,
		Skipped:       nil,
		Cancelled:     nil,
		ChangesReason: "",
		ChangesDetail: "",
		ChangesScope:  nil,
		Name:          "fix", Ran: true, ChangesObserved: true,
		Changes: []report.Change{{Patch: false, Step: 0, Path: "gen/out.txt", Kind: "created"}},
		Tools: []report.ToolRun{{
			RedactedReasons: nil,
			Name:            "", App: report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Incomplete: nil, Invocations: []report.Invocation{{
				RedactedState:      0,
				RedactedExtraction: 0,
				ID:                 "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Findings: nil, Step: 0, OutputTail: "", Changes: []report.Change{{Patch: false, Step: 0, Path: "a.ts", Kind: "modified"}},
			}},
		}},
	}
	tests := []struct {
		name string
		op   report.Operation
		want string
	}{
		{"changed", changed, "fix changed 2 files: a.ts, gen/out.txt"},
		{"none", report.Operation{Success: false, Duration: 0, Skipped: nil, Cancelled: nil, Tools: nil, ChangesReason: "", ChangesDetail: "", ChangesScope: nil, Changes: nil, Name: "fix", Ran: true, ChangesObserved: true}, "fix changed 0 files"},
		{
			"not observed",
			report.Operation{Success: false, Duration: 0, Skipped: nil, Cancelled: nil, Tools: nil, ChangesObserved: false, ChangesScope: nil, Changes: nil, Name: "fix", Ran: true, ChangesReason: "snapshot-failed", ChangesDetail: "no git"},
			"fix changes not observed: snapshot-failed (no git)",
		},
		{
			"partly observed",
			report.Operation{
				Success:         false,
				Duration:        0,
				Skipped:         nil,
				Cancelled:       nil,
				Tools:           nil,
				ChangesObserved: false,
				ChangesDetail:   "",
				ChangesScope:    nil,
				Name:            "fix", Ran: true, ChangesReason: "snapshot-failed",
				Changes: []report.Change{{Patch: false, Step: 0, Path: "a.ts", Kind: "modified"}},
			},
			"fix changes not observed: snapshot-failed; changed at least 1 file: a.ts",
		},
		{"a fix that planned nothing", report.Operation{Success: false, Duration: 0, Skipped: nil, Cancelled: nil, Tools: nil, ChangesObserved: false, ChangesDetail: "", ChangesScope: nil, Changes: nil, Name: "fix", Ran: true, ChangesReason: report.ChangesNoFixTask}, ""},
		{"lint", report.Operation{Success: false, Duration: 0, Skipped: nil, Cancelled: nil, Tools: nil, ChangesObserved: false, ChangesDetail: "", ChangesScope: nil, Changes: nil, Name: "lint", Ran: true, ChangesReason: report.ChangesNoFixTask}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentChanges(tt.op); got != tt.want {
				t.Errorf("agentChanges = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSnapshotBoundedAfterAnInterruption: a snapshot running when the run is
// interrupted goes on for the grace period, then stops.
func TestSnapshotBoundedAfterAnInterruption(t *testing.T) {
	prevTake, prevGrace := takeSnapshot, snapshotGrace
	t.Cleanup(func() { takeSnapshot, snapshotGrace = prevTake, prevGrace })
	snapshotGrace = 50 * time.Millisecond
	started := make(chan struct{}, 2)
	takeSnapshot = func(ctx context.Context, _ string, _ []string) (gitutil.Snapshot, error) {
		started <- struct{}{}
		if len(started) == 1 {
			return gitutil.Snapshot{}, nil
		}
		<-ctx.Done()
		return gitutil.Snapshot{}, ctx.Err()
	}
	task := tooling.Task{Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ID: "fmt::1", ToolName: "fmt", Operation: config.OpFix}
	plan := &tooling.ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []tooling.TaskGroup{{Priority: 0, Tasks: []tooling.Task{task}}}}
	rec := report.NewAccumulator(report.Options{Root: "", Tools: nil, Apps: nil, FailOn: "", Parsers: nil, Secrets: nil}).BeginOperation(string(config.OpFix), plan, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cb := (&sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, rootPath: t.TempDir(),
	}).stepCallback(ctx, config.OpFix, rec)
	time.AfterFunc(20*time.Millisecond, cancel)
	begin := time.Now()
	cb(1, plan.Groups[0].Tasks)
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("the snapshot ran %s after the interruption", took)
	}
	if op := rec.Operation(); op.ChangesObserved || op.ChangesReason != report.ChangesSnapshotFailed {
		t.Errorf("observed %v, reason %q; want the snapshot to have failed", op.ChangesObserved, op.ChangesReason)
	}
}
