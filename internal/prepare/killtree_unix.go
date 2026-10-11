//go:build !windows

package prepare

import (
	"os/exec"
	"syscall"
)

// killTree cancels every process in the step's group: the step runs with a
// dedicated process group (Setpgid), so a negative PID signals the whole
// group — descendants (unzippers, downloaders) do not outlive the timeout.
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	// The step is its own group leader (Setpgid below); -pid is the group.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// Best effort: the direct Kill in the Cancel func follows.
		_ = err
	}
}

// setProcessGroup makes the step its own process group leader, so killTree's
// group signal reaches descendants.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
