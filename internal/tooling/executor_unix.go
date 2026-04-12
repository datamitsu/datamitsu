//go:build !windows

package tooling

import (
	"os/exec"
	"syscall"
	"time"
)

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
