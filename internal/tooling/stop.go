package tooling

import (
	"os/exec"
	"sync/atomic"
)

// trackStop wraps cmd.Cancel and reports whether the cancellation stopped the
// process: the stop went out while the process was still running. Neither the
// delivery nor the exit status can decide alone. A signal reaches a process
// that has exited but is not yet reaped, which would turn a failure that raced
// the cancellation into a stop; and a tool that handles the signal exits with
// a status of its own.
func trackStop(cmd *exec.Cmd) *atomic.Bool {
	stopped := new(atomic.Bool)
	cancel := cmd.Cancel
	if cancel == nil {
		return stopped
	}
	cmd.Cancel = func() error {
		running := cmd.Process != nil && stillRunning(cmd.Process.Pid)
		err := cancel()
		if err == nil && running {
			stopped.Store(true)
		}
		return err
	}
	return stopped
}
