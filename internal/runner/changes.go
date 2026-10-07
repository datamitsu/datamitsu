package runner

import (
	"context"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/gitutil"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// takeSnapshot is gitutil.Take; tests replace it.
var takeSnapshot = gitutil.Take

// snapshotGrace is how long a snapshot may still run once the run was
// interrupted, before or while it runs; a variable so a test can shorten it.
var snapshotGrace = 10 * time.Second

// stepCallback observes what a fix operation changes: a snapshot of the
// working tree before its first step and after every step — the tasks of one
// step ran over disjoint files, so each change belongs to the one whose files
// hold it — recorded on the operation's record. It is nil for any other
// operation, and for a run that records nothing, where no snapshot is taken.
// A snapshot is taken after a failed or cancelled step too: a formatter may
// have written part of its files. When one cannot be taken the operation's
// changes are not observed from there on, and what was seen is kept.
func (sc *sharedContext) stepCallback(ctx context.Context, operation config.OperationType, rec *report.OperationRecord) tooling.StepCallback {
	if operation != config.OpFix || rec == nil {
		return nil
	}
	env := gitutil.Environ()
	snapshot := func() (gitutil.Snapshot, error) {
		// A snapshot after an interruption must still run — a formatter may
		// have written part of its files — but for a while only.
		snapCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		stop := context.AfterFunc(ctx, func() { time.AfterFunc(snapshotGrace, cancel) })
		defer stop()
		return takeSnapshot(snapCtx, sc.rootPath, env)
	}
	before, err := snapshot()
	observing := err == nil
	if observing {
		rec.ObserveChanges()
	} else {
		rec.ChangesNotObserved(report.ChangesSnapshotFailed, err.Error())
	}
	return func(step int, tasks []tooling.Task) {
		ids := make([]string, len(tasks))
		for i, task := range tasks {
			ids[i] = task.ID
		}
		rec.Step(step, ids)
		if !observing {
			return
		}
		after, err := snapshot()
		if err != nil {
			observing = false
			rec.ChangesNotObserved(report.ChangesSnapshotFailed, err.Error())
			return
		}
		rec.Changed(step, before.Diff(after))
		before = after
	}
}
