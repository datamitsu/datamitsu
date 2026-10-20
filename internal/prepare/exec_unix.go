//go:build !windows

package prepare

import (
	"errors"
	"fmt"
	"syscall"
)

// isTransientRenameError reports whether a rename failure is worth retrying.
// Unix rename is atomic and its failures are real, so nothing is transient.
func isTransientRenameError(err error) bool {
	return false
}

// processProbe reports whether a process with the given PID exists. A
// non-positive PID names a process group, not a process: never a lease
// holder.
func processProbe(pid int) error {
	if pid <= 0 {
		return errNoProcess
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return fmt.Errorf("signal %d: %w", pid, err)
	}
	return nil
}

var errNoProcess = errors.New("no such process")

// trackProcessTree is Unix's share of the tree-cancellation contract: the
// process group was set before Start (setProcessGroup), so nothing remains
// to register.
func trackProcessTree(pid int) error { return nil }

// untrackProcessTree drops the registration made by trackProcessTree.
func untrackProcessTree(pid int) {}
