//go:build !windows

package storelock

import (
	"context"
	"fmt"
	"os"
	"syscall"
)

// lockFile acquires an exclusive flock on name, waiting only while ctx is
// alive. The goroutine-based wait makes the acquisition cancelable: a plain
// blocking LOCK_EX cannot be interrupted, so the lock is attempted in a
// watcher goroutine and the caller unblocks on whichever comes first.
func lockFile(ctx context.Context, name string) (release func(), err error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", name, err)
	}

	type result struct {
		release func()
		err     error
	}
	acquired := make(chan result, 1)
	go func() {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			acquired <- result{nil, fmt.Errorf("lock %s: %w", name, err)}
			_ = f.Close()
			return
		}
		var once releaseOnce
		acquired <- result{once.do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}), nil}
	}()

	select {
	case r := <-acquired:
		return r.release, r.err
	case <-ctx.Done():
		// The watcher goroutine still holds the channel's buffer; when its
		// flock eventually succeeds (or fails) it releases the file itself
		// through the buffered send being dropped and the once running only
		// in the goroutine. Drain is impossible without leaking the goroutine
		// until the holder finishes, so instead the goroutine's release is
		// invoked by receiving from the buffered channel in a detached
		// goroutine: the lock is dropped the moment it is acquired.
		go func() {
			if r := <-acquired; r.err == nil {
				r.release()
			}
		}()
		return nil, fmt.Errorf("waiting for store lock %s: %w", name, ctx.Err())
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
