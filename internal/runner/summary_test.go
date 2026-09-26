package runner

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
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
			name:      "both ran",
			summaries: []opSummary{{op: config.OpFix, durationMs: 400}, {op: config.OpLint, durationMs: 1500}},
			elapsed:   2300,
			want:      "┗━ check · done in 2.30s · fix 400ms · lint 1.50s · setup 400ms",
		},
		{
			name:      "lint not run",
			summaries: []opSummary{{op: config.OpFix, durationMs: 400}},
			elapsed:   700,
			want:      "┗━ check · done in 700ms · fix 400ms · lint not run · setup 300ms",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &sharedContext{summaries: tt.summaries}
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
			name: "complete",
			summaries: []opSummary{
				{op: config.OpFix, tools: 1, runs: 2, skipped: 1},
				{op: config.OpLint, tools: 2, runs: 3, failed: 1},
			},
			wantComplete: true, wantRuns: 5,
		},
		{
			name:      "an operation did not run",
			summaries: []opSummary{{op: config.OpFix, tools: 1, runs: 1, failed: 1}},
			wantRuns:  1,
		},
		{
			name: "a task was stopped",
			summaries: []opSummary{
				{op: config.OpFix, tools: 1, runs: 1},
				{op: config.OpLint, tools: 1, runs: 1, cancelled: 2},
			},
			wantRuns: 2, wantCancel: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &recordingSink{}
			ui.SetEventSink(sink, true)
			defer ui.SetEventSink(nil, false)

			sc := &sharedContext{summaries: tt.summaries}
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
