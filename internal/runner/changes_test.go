package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/gitutil"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// TestStepCallbackStopsObservingOnAFailedSnapshot: a snapshot that cannot be
// taken — first or later — leaves the fix's changes unobserved from there on,
// with the reason; steps are still recorded. lint observes nothing.
func TestStepCallbackStopsObservingOnAFailedSnapshot(t *testing.T) {
	task := tooling.Task{ID: "fmt::1", ToolName: "fmt", Operation: config.OpFix}
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{task}}}}
	tests := []struct {
		name      string
		failAt    int
		observed  bool
		reason    string
		wantSteps int
	}{
		{name: "every snapshot taken", failAt: -1, observed: true, wantSteps: 1},
		{name: "the first fails", failAt: 0, reason: report.ChangesSnapshotFailed, wantSteps: 1},
		{name: "a later one fails", failAt: 1, reason: report.ChangesSnapshotFailed, wantSteps: 1},
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

			acc := report.NewAccumulator(report.Options{})
			rec := acc.BeginOperation(string(config.OpFix), plan, nil)
			sc := &sharedContext{rootPath: t.TempDir()}
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
		Name: "fix", Ran: true, ChangesObserved: true,
		Changes: []report.Change{{Path: "gen/out.txt", Kind: "created"}},
		Tools:   []report.ToolRun{{Invocations: []report.Invocation{{Changes: []report.Change{{Path: "a.ts", Kind: "modified"}}}}}},
	}
	tests := []struct {
		name string
		op   report.Operation
		want string
	}{
		{"changed", changed, "fix changed 2 files: a.ts, gen/out.txt"},
		{"none", report.Operation{Name: "fix", Ran: true, ChangesObserved: true}, "fix changed 0 files"},
		{
			"not observed",
			report.Operation{Name: "fix", Ran: true, ChangesReason: "snapshot-failed", ChangesDetail: "no git"},
			"fix changes not observed: snapshot-failed (no git)",
		},
		{
			"partly observed",
			report.Operation{
				Name: "fix", Ran: true, ChangesReason: "snapshot-failed",
				Changes: []report.Change{{Path: "a.ts", Kind: "modified"}},
			},
			"fix changes not observed: snapshot-failed; changed at least 1 file: a.ts",
		},
		{"a fix that planned nothing", report.Operation{Name: "fix", Ran: true, ChangesReason: report.ChangesNoFixTask}, ""},
		{"lint", report.Operation{Name: "lint", Ran: true, ChangesReason: report.ChangesNoFixTask}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentChanges(tt.op); got != tt.want {
				t.Errorf("agentChanges = %q, want %q", got, tt.want)
			}
		})
	}
}
