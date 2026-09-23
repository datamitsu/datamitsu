//go:build windows

package managedconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"

	"golang.org/x/sys/windows"
)

// lockDatamitsuDir serializes the writers of {gitRoot}/.datamitsu/. Windows
// cannot lock a directory handle the way flock does on Unix, so the lock is a
// file under the cache root keyed by the git root, never a file in the
// repository.
func lockDatamitsuDir(gitRoot string) (release func(), err error) {
	dir := filepath.Join(env.GetCachePath(), "datamitsu-dir-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, hashutil.XXH3Hex([]byte(gitRoot))+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", gitRoot, err)
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
