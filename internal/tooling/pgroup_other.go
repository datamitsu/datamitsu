//go:build !linux && !windows

package tooling

import "syscall"

// groupAlive reports whether group pgid still has a process. Here a group of
// zombies counts as alive, so the kill waits out the grace for it.
func groupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}
