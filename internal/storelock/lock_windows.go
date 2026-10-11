//go:build windows

package storelock

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile acquires an exclusive LockFileEx range lock on name, waiting only
// while ctx is alive. Unlike the Unix flock, LockFileEx supports
// LOCKFILE_FAIL_IMMEDIATELY, so the wait loop can poll with cancellation
// instead of a watcher goroutine. Placement is atomic either way; the lock
// only prevents duplicate work, so a poll interval of 50ms is fine.
func lockFile(ctx context.Context, name string) (release func(), err error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", name, err)
	}

	// Lock the first byte of the file. A zero-length range is a no-op, and
	// every lock file is created empty.
	for {
		overlapped := new(windows.Overlapped)
		err := windows.LockFileEx(windows.Handle(f.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, 1, 0, overlapped)
		if err == nil {
			var once releaseOnce
			return once.do(func() {
				_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped)
				_ = f.Close()
			}), nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for store lock %s: %w", name, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// releaseOnce runs fn exactly once, so a double release is harmless.
type releaseOnce struct{ done bool }

func (o *releaseOnce) do(fn func()) func() {
	return func() {
		if o.done {
			return
		}
		o.done = true
		fn()
	}
}
