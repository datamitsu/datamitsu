//go:build !windows

package tooling

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// endedOnItsOwn reports whether a process exited with a status of its own
// rather than to a signal. The stop signal can be delivered to a process that
// has already exited but is not yet reaped, so a delivered signal alone does
// not prove the cancellation ended it. An exit status of 128+signal is how a
// wrapper reports a signal it received, and counts as a signal.
func endedOnItsOwn(state *os.ProcessState) bool {
	if state == nil {
		return false
	}
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok || ws.Signaled() {
		return false
	}
	switch ws.ExitStatus() {
	case 128 + int(syscall.SIGINT), 128 + int(syscall.SIGKILL), 128 + int(syscall.SIGTERM):
		return false
	}
	return true
}

func setupProcessGroupCleanup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0

	// Send SIGTERM first for graceful shutdown; WaitDelay triggers SIGKILL after timeout
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				return syscall.Kill(-pgid, syscall.SIGTERM)
			}
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}
