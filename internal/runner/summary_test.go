package runner

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	zerovalue_time "time"

	zerovalue_cienv "github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/config"
	zerovalue_tooling "github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

func TestCommandName(t *testing.T) {
	tests := []struct {
		ops  []config.OperationType
		want string
	}{
		{ops: []config.OperationType{config.OpFix, config.OpLint}, want: "check"},
		{ops: []config.OperationType{config.OpFix}, want: "fix"},
		{ops: []config.OperationType{config.OpLint}, want: "lint"},
		{ops: nil, want: ""},
	}
	for _, tt := range tests {
		if got := commandName(tt.ops); got != tt.want {
			t.Errorf("commandName(%v) = %q, want %q", tt.ops, got, tt.want)
		}
	}
}

// The closing line names every operation: its execution time when it ran,
// "not run" when it did not, and the rest of the wall clock as setup.
func TestPrintRunClosing(t *testing.T) {
	t.Setenv("CI", "true")
	ops := []config.OperationType{config.OpFix, config.OpLint}
	tests := []struct {
		name      string
		summaries []opSummary
		elapsed   int64
		want      string
	}{
		{
			name: "both ran",
			summaries: []opSummary{{
				incomplete: false,
				unstarted:  false,
				tools:      0, runs: 0, failed: 0, skipped: 0, cancelled: 0, partial: 0, op: config.OpFix, durationMs: 400,
			}, {
				incomplete: false,
				unstarted:  false,
				tools:      0, runs: 0, failed: 0, skipped: 0, cancelled: 0, partial: 0, op: config.OpLint, durationMs: 1500,
			}},
			elapsed: 2300,
			want:    "┗━ check · done in 2.30s · fix 400ms · lint 1.50s · setup 400ms",
		},
		{
			name: "lint not run",
			summaries: []opSummary{{
				incomplete: false,
				unstarted:  false,
				tools:      0, runs: 0, failed: 0, skipped: 0, cancelled: 0, partial: 0, op: config.OpFix, durationMs: 400,
			}},
			elapsed: 700,
			want:    "┗━ check · done in 700ms · fix 400ms · lint not run · setup 300ms",
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
				cfg:                  nil, rootPath: "", cwdPath: "", selection: zerovalue_tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, narrowed: nil, failFast: false, afterFailedFix: false, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, summaries: tt.summaries,
			}
			out := captureStdout(t, func() { sc.printRunClosing("check", ops, tt.elapsed) })
			if !strings.HasPrefix(out, "\n"+tt.want+" ━") {
				t.Errorf("printRunClosing() printed %q, want %q", out, tt.want)
			}
		})
	}
}

type recordingSink struct {
	mu     sync.Mutex
	events []uievent.Event
}

func (r *recordingSink) Emit(e uievent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// The run-level done sums the operations that ran; a run is complete only when
// every planned operation ran and no task was stopped.
func TestEmitRunDone(t *testing.T) {
	ops := []config.OperationType{config.OpFix, config.OpLint}
	tests := []struct {
		name         string
		summaries    []opSummary
		success      bool
		wantComplete bool
		wantRuns     int
		wantCancel   int
	}{
		{
			success:    false,
			wantCancel: 0,
			name:       "complete",
			summaries: []opSummary{
				{
					incomplete: false,
					unstarted:  false,
					failed:     0, cancelled: 0, partial: 0, durationMs: 0, op: config.OpFix, tools: 1, runs: 2, skipped: 1,
				},
				{
					incomplete: false,
					unstarted:  false,
					skipped:    0, cancelled: 0, partial: 0, durationMs: 0, op: config.OpLint, tools: 2, runs: 3, failed: 1,
				},
			},
			wantComplete: true, wantRuns: 5,
		},
		{
			success:      false,
			wantComplete: false,
			wantCancel:   0,
			name:         "an operation did not run",
			summaries: []opSummary{{
				incomplete: false,
				unstarted:  false,
				skipped:    0, cancelled: 0, partial: 0, durationMs: 0, op: config.OpFix, tools: 1, runs: 1, failed: 1,
			}},
			wantRuns: 1,
		},
		{
			success:      false,
			wantComplete: false,
			wantCancel:   0,
			name:         "a per-file task stopped at a failing file",
			summaries: []opSummary{
				{
					incomplete: false,
					unstarted:  false,
					failed:     0, skipped: 0, cancelled: 0, partial: 0, durationMs: 0, op: config.OpFix, tools: 1, runs: 1,
				},
				{
					incomplete: false,
					unstarted:  false,
					skipped:    0, cancelled: 0, durationMs: 0, op: config.OpLint, tools: 1, runs: 1, failed: 1, partial: 1,
				},
			},
			wantRuns: 2,
		},
		{
			success:      false,
			wantComplete: false,
			name:         "a task was stopped",
			summaries: []opSummary{
				{
					incomplete: false,
					unstarted:  false,
					failed:     0, skipped: 0, cancelled: 0, partial: 0, durationMs: 0, op: config.OpFix, tools: 1, runs: 1,
				},
				{
					incomplete: false,
					unstarted:  false,
					failed:     0, skipped: 0, partial: 0, durationMs: 0, op: config.OpLint, tools: 1, runs: 1, cancelled: 2,
				},
			},
			wantRuns: 2, wantCancel: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &recordingSink{mu: sync.Mutex{}, events: nil}
			ui.SetEventSink(sink, true)
			defer ui.SetEventSink(nil, false)

			sc := &sharedContext{
				preparedTools:        false,
				parserPrewarmed:      false,
				bundledFixFindings:   nil,
				bundledFixChanges:    nil,
				bundledLintFindings:  nil,
				bundledLintErr:       nil,
				completedBundledLint: false,
				cfg:                  nil, rootPath: "", cwdPath: "", selection: zerovalue_tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, narrowed: nil, failFast: false, afterFailedFix: false, fileScoped: false, report: nil, secrets: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, summaries: tt.summaries,
			}
			sc.emitRunDone("check", ops, 1234, tt.success)

			if len(sink.events) != 1 {
				t.Fatalf("emitted %d events, want 1", len(sink.events))
			}
			e := sink.events[0]
			if e.Type != uievent.TypeDone || !strings.HasPrefix(e.OpID, "cmd-") || e.Op != "check" || e.DurationMs != 1234 {
				t.Errorf("event = %+v, want a check done with a cmd- op id and the wall clock", e)
			}
			if e.Runs != tt.wantRuns || e.Cancelled == nil || *e.Cancelled != tt.wantCancel ||
				e.Complete == nil || *e.Complete != tt.wantComplete || e.Success == nil || *e.Success != tt.success {
				var buf bytes.Buffer
				_ = json.NewEncoder(&buf).Encode(e)
				t.Errorf("event = %s, want runs %d, cancelled %d, complete %v", buf.String(), tt.wantRuns, tt.wantCancel, tt.wantComplete)
			}
		})
	}
}
