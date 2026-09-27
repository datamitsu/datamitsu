//go:build linux

package tooling

import (
	"errors"

	"golang.org/x/sys/unix"
)

// stillRunning reports whether the child pid has not exited, without reaping
// it. The kernel leaves Signo zero when the child has not exited; an error
// means it is already reaped.
func stillRunning(pid int) bool {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err == nil && info.Signo == 0
	}
}
