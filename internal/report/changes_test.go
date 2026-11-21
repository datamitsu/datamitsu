package report

import (
	"reflect"
	"testing"
	zerovalue_time "time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/gitutil"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func ranFile(task tooling.Task, n int, file, patch string) tooling.ExecutionResult {
	id := task.ID + "#1"
	return tooling.ExecutionResult{
		Output:           "",
		Error:            nil,
		Duration:         0,
		Command:          "",
		ExitCode:         0,
		WorkingDir:       "",
		RelativeDir:      "",
		Scope:            "",
		Batch:            false,
		Cancelled:        false,
		FailureReason:    0,
		StartedAt:        zerovalue_time.Time{},
		EndedAt:          zerovalue_time.Time{},
		FilesNotRun:      0,
		UnparsedFailures: nil,
		CapturedStdout:   "",
		Diagnostics:      nil,
		ParseFailed:      false,
		FailOn:           "",
		Cached:           false,
		WholeUnit:        false,
		UnitDir:          "",
		ToolName:         task.ToolName, TaskID: task.ID, Success: true,
		Processes: []tooling.ProcessResult{{Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, ID: id, Files: []string{abs(file)}, State: tooling.ProcessRan, ExitCode: new(0), Success: true}},
		Files:     []string{abs(file)},
		FileResults: []tooling.FileResult{
			{Edits: nil, File: abs(file), State: tooling.FileRan, ProcessID: id, Success: true, ExitCode: new(0), Patch: patch},
		},
	}
}

// TestChangesAttribution: a change goes to the invocation of the step that
// made it whose files hold it, with its patch when one was captured; a file no
// invocation of the step was given goes to the operation.
func TestChangesAttribution(t *testing.T) {
	a := perFileTask("hadolint:a:1", "a/Dockerfile")
	b := perFileTask("hadolint:b:2", "b/Dockerfile")
	plan := &tooling.ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []tooling.TaskGroup{{Priority: 0, Tasks: []tooling.Task{a, b}}}}
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

	op := acc.Build(BuildInfo{Version: "", Configuration: "", StartedAt: zerovalue_time.Time{}, EndedAt: zerovalue_time.Time{}, Selection: Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil}, FailFast: false, Exports: nil, CI: CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""}}).Operations[0]
	if !op.ChangesObserved || op.ChangesReason != "" || op.ChangesScope == nil || *op.ChangesScope != ObservedScope {
		t.Fatalf("changes observed = %v, reason %q, scope %v", op.ChangesObserved, op.ChangesReason, op.ChangesScope)
	}
	invocations := op.Tools[0].Invocations
	if invocations[0].Step != 1 || invocations[1].Step != 2 {
		t.Errorf("steps = %d, %d; want 1, 2", invocations[0].Step, invocations[1].Step)
	}
	if want := []Change{{Path: "a/Dockerfile", Kind: "modified", Patch: true, Step: 1}}; !reflect.DeepEqual(invocations[0].Changes, want) {
		t.Errorf("first invocation's changes = %v, want %v", invocations[0].Changes, want)
	}
	if want := []Change{{Patch: false, Path: "b/Dockerfile", Kind: "reverted", Step: 2}}; !reflect.DeepEqual(invocations[1].Changes, want) {
		t.Errorf("second invocation's changes = %v, want %v", invocations[1].Changes, want)
	}
	// b/Dockerfile changed in step 1, where no invocation was given it.
	want := []Change{{Patch: false, Path: "b/Dockerfile", Kind: "modified", Step: 1}, {Patch: false, Path: "gen/out.txt", Kind: "created", Step: 2}}
	if !reflect.DeepEqual(op.Changes, want) {
		t.Errorf("operation changes = %v, want %v", op.Changes, want)
	}
	if got := op.AllChanges(); len(got) != 3 || got[0].Path != "a/Dockerfile" || got[2].Path != "gen/out.txt" {
		t.Errorf("AllChanges = %v, want each file once, sorted", got)
	}
}

func TestChangesNotObserved(t *testing.T) {
	task := perFileTask("hadolint:a:1", "a/Dockerfile")
	plan := &tooling.ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []tooling.TaskGroup{{Priority: 0, Tasks: []tooling.Task{task}}}}
	tests := []struct {
		name       string
		record     func(*OperationRecord)
		reason     string
		changes    int
		scope      bool
		operations []string
	}{
		{changes: 0, scope: false, operations: nil, name: "lint", record: func(*OperationRecord) {}, reason: ChangesNoFixTask},
		{changes: 0, scope: false, operations: nil, name: "setup failed", record: func(r *OperationRecord) { r.ChangesNotObserved(ChangesNotExecuted, "") }, reason: ChangesNotExecuted},
		{
			operations: nil, name: "a later snapshot failed", record: func(r *OperationRecord) {
				r.ObserveChanges()
				r.Changed(1, []gitutil.Change{{Path: "a/Dockerfile", Kind: gitutil.Modified}})
				r.ChangesNotObserved(ChangesSnapshotFailed, "status failed")
			}, reason: ChangesSnapshotFailed, changes: 1, scope: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := NewAccumulator(testOptions())
			tt.record(acc.BeginOperation(string(config.OpFix), plan, nil))
			acc.NotRun(string(config.OpLint))
			run := acc.Build(BuildInfo{Version: "", Configuration: "", StartedAt: zerovalue_time.Time{}, EndedAt: zerovalue_time.Time{}, Selection: Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil}, FailFast: false, Exports: nil, CI: CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""}})
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

// TestAllChangesIsTheNetChange: a file several steps changed is listed once,
// with what changed from before the first step to after the last.
func TestAllChangesIsTheNetChange(t *testing.T) {
	op := Operation{
		Name:            "",
		Ran:             false,
		Success:         false,
		Duration:        0,
		Skipped:         nil,
		Cancelled:       nil,
		ChangesObserved: false,
		ChangesReason:   "",
		ChangesDetail:   "",
		ChangesScope:    nil,
		Changes: []Change{
			{Patch: false, Path: "tmp.txt", Kind: "created", Step: 1},
			{Patch: false, Path: "gen.txt", Kind: "created", Step: 1},
			{Patch: false, Path: "old.txt", Kind: "modified", Step: 1},
			{Patch: false, Path: "moved.txt", Kind: "deleted", Step: 1},
		},
		Tools: []ToolRun{{
			Name:           "",
			App:            AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
			Parser:         nil,
			FailOn:         "",
			GateActive:     false,
			Category:       "",
			Complete:       false,
			Incomplete:     nil,
			mayHoldSecrets: false, Invocations: []Invocation{{
				ID:          "",
				TaskID:      "",
				Dir:         "",
				Scope:       "",
				Granularity: "",
				Arity:       "",
				Coverage:    "",
				WholeUnit:   false,
				State:       "",
				ExitCode:    nil,
				Success:     false,
				FailureKind: "",
				Duration:    0,
				Extraction:  "",
				Provenance:  "",
				Files:       nil,
				Findings:    nil,
				Step:        0,
				OutputTail:  "", Changes: []Change{
					{Patch: false, Path: "tmp.txt", Kind: "deleted", Step: 2},
					{Path: "gen.txt", Kind: "modified", Step: 2, Patch: true},
					{Patch: false, Path: "old.txt", Kind: "deleted", Step: 2},
					{Patch: false, Path: "moved.txt", Kind: "created", Step: 2},
				},
			}},
		}},
	}
	want := []Change{
		{Path: "gen.txt", Kind: "created", Step: 2, Patch: true},
		{Patch: false, Path: "moved.txt", Kind: "modified", Step: 2},
		{Patch: false, Path: "old.txt", Kind: "deleted", Step: 2},
	}
	if got := op.AllChanges(); !reflect.DeepEqual(got, want) {
		t.Errorf("AllChanges =\n%v\nwant\n%v", got, want)
	}
}

// TestChangesSkipInvocationsThatNeverRan: a task of the step that never
// started changed nothing; a change of its file is the operation's.
func TestChangesSkipInvocationsThatNeverRan(t *testing.T) {
	task := perFileTask("hadolint:a:1", "a/Dockerfile")
	plan := &tooling.ExecutionPlan{ConfigName: "", FailOn: "", Skipped: nil, Groups: []tooling.TaskGroup{{Priority: 0, Tasks: []tooling.Task{task}}}}
	acc := NewAccumulator(testOptions())
	rec := acc.BeginOperation(string(config.OpFix), plan, nil)
	rec.ObserveChanges()
	rec.Step(1, []string{task.ID})
	rec.Changed(1, []gitutil.Change{{Path: "a/Dockerfile", Kind: gitutil.Modified}})
	op := acc.Build(BuildInfo{Version: "", Configuration: "", StartedAt: zerovalue_time.Time{}, EndedAt: zerovalue_time.Time{}, Selection: Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil}, FailFast: false, Exports: nil, CI: CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""}}).Operations[0]
	if inv := op.Tools[0].Invocations[0]; inv.State != "not-started" || len(inv.Changes) != 0 || len(op.Changes) != 1 {
		t.Errorf("invocation %s changes %v, operation changes %v; want the change on the operation", inv.State, inv.Changes, op.Changes)
	}
}
