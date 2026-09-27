//go:build freebsd

package tooling

import "golang.org/x/sys/unix"

// pPID is P_PID of idtype_t (sys/wait.h).
const pPID = 0

// stillRunning reports whether the child pid has not exited, without reaping
// it. wait6 returns zero while the child has not exited; an error means it is
// already reaped.
func stillRunning(pid int) bool {
	for {
		r, _, errno := unix.Syscall6(unix.SYS_WAIT6, pPID, uintptr(pid), 0, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, 0, 0)
		if errno == unix.EINTR {
			continue
		}
		return errno == 0 && r == 0
	}
}
