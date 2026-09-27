package tooling

import (
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

type stopTracker struct {
	// requested is when the stop was sent, in Unix nanoseconds; zero if never.
	requested atomic.Int64
	stopped   atomic.Bool
}

// trackStop wraps cmd.Cancel and records whether the cancellation stopped the
// process: the stop went out while the process was still running. Neither the
// delivery nor the exit status can decide alone. A signal reaches a process
// that has exited but is not yet reaped, which would turn a failure that raced
// the cancellation into a stop; and a tool that handles the signal exits with
// a status of its own. A process that exits on its own between the check and
// the signal still counts as stopped: nothing tells it from one that handled
// the signal, and the window is one system call wide.
func trackStop(cmd *exec.Cmd) *stopTracker {
	t := &stopTracker{}
	cancel := cmd.Cancel
	if cancel == nil {
		return t
	}
	cmd.Cancel = func() error {
		t.requested.Store(time.Now().UnixNano())
		running := cmd.Process != nil && stillRunning(cmd.Process.Pid)
		err := cancel()
		switch {
		case err != nil:
			return err
		case !running:
			// The signal still reaches what the process left in its group, but
			// its own exit stands: without ErrProcessDone, os/exec would turn a
			// success into the context's error.
			return os.ErrProcessDone
		}
		t.stopped.Store(true)
		return nil
	}
	return t
}
