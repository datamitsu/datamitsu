//go:build unix

package managedconfig

import (
	"fmt"
	"os"
	"syscall"
)

// lockDatamitsuDir serializes the writers of {gitRoot}/.datamitsu/ — the link
// rebuild (init, and exec of a lazily installed link-app) and the internal
// config writer — so a rebuild never carries over a configs/ directory that
// another process is halfway through replacing. It locks the git root
// directory itself rather than a lock file, which would be one more file in
// the repository.
func lockDatamitsuDir(gitRoot string) (release func(), err error) {
	dir, err := os.Open(gitRoot)
	if err != nil {
		return nil, fmt.Errorf("open %s for locking: %w", gitRoot, err)
	}
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("lock %s: %w", gitRoot, err)
	}
	return func() {
		_ = syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
		_ = dir.Close()
	}, nil
}
