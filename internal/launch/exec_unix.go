//go:build !windows

package launch

import "syscall"

func execAppCommand(path string, argv, environ []string) int {
	// #nosec G204 G702 -- path comes from a validated content-addressed launcher record.
	if err := syscall.Exec(path, argv, environ); err != nil {
		return 126
	}
	return 0
}
