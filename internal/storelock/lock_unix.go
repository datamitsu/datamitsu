//go:build !windows

package storelock

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockFile acquires an exclusive flock. The retry loop permits cancellation
// without leaving a goroutine holding a lock after its caller has returned.
func lockFile(ctx context.Context, name string) (func(), error) {
	return lockFileWithWait(ctx, name, nil)
}

func lockFileWithWait(ctx context.Context, name string, onWait func()) (func(), error) {
	return lockMode(ctx, name, syscall.LOCK_EX, onWait)
}

func sharedLockFile(ctx context.Context, name string) (func(), error) {
	return lockMode(ctx, name, syscall.LOCK_SH, nil)
}

func lockMode(ctx context.Context, name string, mode int, onWait func()) (func(), error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", name, err)
	}
	waiting := false
	for {
		err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return once(func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }), nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
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
