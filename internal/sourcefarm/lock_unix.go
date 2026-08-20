//go:build unix

package sourcefarm

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock on an already-open file without
// blocking. It reports ok=false when another process holds the lock, and an
// error only for conditions the caller cannot poll its way out of.
func tryLockFile(file *os.File) (ok bool, err error) {
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return false, fmt.Errorf("flock %s: %w", file.Name(), err)
	}
}

// lockFile blocks until the exclusive advisory lock is held.
func lockFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock %s: %w", file.Name(), err)
	}
	return nil
}

// unlockFile releases the advisory lock.
func unlockFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
