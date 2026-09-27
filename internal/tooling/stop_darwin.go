//go:build darwin

package tooling

import "golang.org/x/sys/unix"

// szomb is the p_stat of a process that has exited and is not yet reaped
// (sys/proc.h).
const szomb = 5

// stillRunning reports whether the child pid has not exited, without reaping
// it. A reaped process has no entry, which the sysctl reports as an error.
func stillRunning(pid int) bool {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && info.Proc.P_stat != szomb
}
