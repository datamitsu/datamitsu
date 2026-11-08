//go:build windows

package storelock

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile acquires an exclusive range lock with cancellation polling.
func lockFile(ctx context.Context, name string) (func(), error) {
	return lockFileWithWait(ctx, name, nil)
}

func lockFileWithWait(ctx context.Context, name string, onWait func()) (func(), error) {
	return lockMode(ctx, name, true, onWait)
}

func sharedLockFile(ctx context.Context, name string) (func(), error) {
	return lockMode(ctx, name, false, nil)
}

func lockMode(ctx context.Context, name string, exclusive bool, onWait func()) (func(), error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", name, err)
	}
	waiting := false
	for {
		ov := new(windows.Overlapped)
		flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
		if exclusive {
			flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
		}
		err = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, ov)
		if err == nil {
			return once(func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ov); _ = f.Close() }), nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", name, err)
		}
		if !waiting {
			waiting = true
			if onWait != nil {
				onWait()
			}
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for store lock %s: %w", name, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
