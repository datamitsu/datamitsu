//go:build !windows

package tooling

import (
	"os/exec"
	"syscall"
	"time"
)

// stopGrace is how long a stopped tool has to exit after SIGTERM before it,
// and every process of its group, is killed.
var stopGrace = 5 * time.Second

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
	cmd.WaitDelay = stopGrace
}

// killGroupAfterGrace kills what is left of a stopped tool's process group once
// the grace of its SIGTERM has passed. WaitDelay kills only the process
// datamitsu started, so a descendant that ignores SIGTERM would outlive the run.
func killGroupAfterGrace(pgid int, stoppedAt time.Time) {
	for deadline := stoppedAt.Add(stopGrace); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !groupAlive(pgid) {
			return
		}
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
