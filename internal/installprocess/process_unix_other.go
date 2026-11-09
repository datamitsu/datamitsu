//go:build !windows && !linux

package installprocess

import "syscall"

func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}
