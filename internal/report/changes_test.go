package report

import (
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/gitutil"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func ranFile(task tooling.Task, n int, file, patch string) tooling.ExecutionResult {
	id := task.ID + "#1"
	return tooling.ExecutionResult{
		ToolName: task.ToolName, TaskID: task.ID, Success: true,
		Processes: []tooling.ProcessResult{{ID: id, Files: []string{abs(file)}, State: tooling.ProcessRan, ExitCode: new(0), Success: true}},
		Files:     []string{abs(file)},
		FileResults: []tooling.FileResult{
			{File: abs(file), State: tooling.FileRan, ProcessID: id, Success: true, ExitCode: new(0), Patch: patch},
		},
	}
}

// TestChangesAttribution: a change goes to the invocation of the step that
// made it whose files hold it, with its patch when one was captured; a file no
// invocation of the step was given goes to the operation.
func TestChangesAttribution(t *testing.T) {
	a := perFileTask("hadolint:a:1", "a/Dockerfile")
	b := perFileTask("hadolint:b:2", "b/Dockerfile")
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{a, b}}}}
	acc := NewAccumulator(testOptions())
	rec := acc.BeginOperation(string(config.OpFix), plan, nil)
	rec.AddTask(ranFile(a, 1, "a/Dockerfile", "--- a/a/Dockerfile\n"))
	rec.AddTask(ranFile(b, 1, "b/Dockerfile", ""))
	rec.ObserveChanges()
	rec.Step(1, []string{a.ID})
	rec.Changed(1, []gitutil.Change{{Path: "a/Dockerfile", Kind: gitutil.Modified}, {Path: "b/Dockerfile", Kind: gitutil.Modified}})
	rec.Step(2, []string{b.ID})
	rec.Changed(2, []gitutil.Change{{Path: "b/Dockerfile", Kind: gitutil.Reverted}, {Path: "gen/out.txt", Kind: gitutil.Created}})
	rec.End(true, 1)

	op := acc.Build(BuildInfo{}).Operations[0]
	if !op.ChangesObserved || op.ChangesReason != "" || op.ChangesScope == nil || *op.ChangesScope != ObservedScope {
		t.Fatalf("changes observed = %v, reason %q, scope %v", op.ChangesObserved, op.ChangesReason, op.ChangesScope)
	}
	invocations := op.Tools[0].Invocations
	if invocations[0].Step != 1 || invocations[1].Step != 2 {
		t.Errorf("steps = %d, %d; want 1, 2", invocations[0].Step, invocations[1].Step)
	}
	if want := []Change{{Path: "a/Dockerfile", Kind: "modified", Patch: true}}; !reflect.DeepEqual(invocations[0].Changes, want) {
		t.Errorf("first invocation's changes = %v, want %v", invocations[0].Changes, want)
	}
	if want := []Change{{Path: "b/Dockerfile", Kind: "reverted"}}; !reflect.DeepEqual(invocations[1].Changes, want) {
		t.Errorf("second invocation's changes = %v, want %v", invocations[1].Changes, want)
	}
	// b/Dockerfile changed in step 1, where no invocation was given it.
	want := []Change{{Path: "b/Dockerfile", Kind: "modified"}, {Path: "gen/out.txt", Kind: "created"}}
	if !reflect.DeepEqual(op.Changes, want) {
		t.Errorf("operation changes = %v, want %v", op.Changes, want)
	}
	if got := op.AllChanges(); len(got) != 3 || got[0].Path != "a/Dockerfile" || got[2].Path != "gen/out.txt" {
		t.Errorf("AllChanges = %v, want each file once, sorted", got)
	}
}

func TestChangesNotObserved(t *testing.T) {
	task := perFileTask("hadolint:a:1", "a/Dockerfile")
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{task}}}}
	tests := []struct {
		name       string
		record     func(*OperationRecord)
		reason     string
		changes    int
		scope      bool
		operations []string
	}{
		{name: "lint", record: func(*OperationRecord) {}, reason: ChangesNoFixTask},
		{name: "setup failed", record: func(r *OperationRecord) { r.ChangesNotObserved(ChangesNotExecuted, "") }, reason: ChangesNotExecuted},
		{name: "a later snapshot failed", record: func(r *OperationRecord) {
			r.ObserveChanges()
			r.Changed(1, []gitutil.Change{{Path: "a/Dockerfile", Kind: gitutil.Modified}})
			r.ChangesNotObserved(ChangesSnapshotFailed, "status failed")
		}, reason: ChangesSnapshotFailed, changes: 1, scope: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := NewAccumulator(testOptions())
			tt.record(acc.BeginOperation(string(config.OpFix), plan, nil))
			acc.NotRun(string(config.OpLint))
			run := acc.Build(BuildInfo{})
			op := run.Operations[0]
			if op.ChangesObserved || op.ChangesReason != tt.reason || len(op.AllChanges()) != tt.changes || (op.ChangesScope != nil) != tt.scope {
				t.Errorf("op = observed %v, reason %q, changes %v, scope %v", op.ChangesObserved, op.ChangesReason, op.AllChanges(), op.ChangesScope)
			}
			if lint := run.Operations[1]; lint.ChangesReason != ChangesNotRun {
				t.Errorf("an operation never reached: reason %q, want not-run", lint.ChangesReason)
			}
		})
	}
}
