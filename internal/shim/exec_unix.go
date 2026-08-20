//go:build !windows

package shim

import (
	"fmt"
	"syscall"
)

// execProcess replaces this process with the target.
//
// syscall.Exec, not a child process: the shell's job control, the signal
// disposition, stdin/stdout/stderr wiring and the exit code all belong to the
// tool the user typed, and a supervising parent would have to forward every one
// of them imperfectly. It is also why the shim costs one process rather than
// two.
func execProcess(path string, argv, environ []string) error {
	// #nosec G204 -- path comes from the farm manifest datamitsu itself wrote,
	// and replacing this process with it is the entire point of the shim.
	if err := syscall.Exec(path, argv, environ); err != nil {
		return fmt.Errorf("exec %s: %w", path, err)
	}
	return nil
}
