//go:build unix

package ocibundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

// lockSubtree takes an exclusive inter-process lock for one subtree so two
// parallel datamitsu invocations never unpack the same subtree concurrently.
// Correctness does not depend on it (the final placement is an atomic rename
// onto a content-addressed path); the lock only prevents duplicated work.
// Lock files live under the CACHE root, not the store: a concurrent
// `store clear` would otherwise delete a held lock file and let a third
// process lock a fresh inode for the same subtree.
//
// A PREPARED subtree locks through internal/storelock instead — the very
// lock the prepare engine holds while placing the same state directory —
// with the same canonical absolute arguments (store root, absolute state
// dir), so a seed moving a markerless remnant aside and a run placing a
// fresh state serialize against each other instead of racing.
func lockSubtree(ctx context.Context, storeRoot, subtree string) (release func(), err error) {
	if strings.HasPrefix(subtree, preparedSubtreeRoot) {
		storeAbs, absErr := filepath.Abs(storeRoot)
		if absErr != nil {
			return nil, fmt.Errorf("absolutize store root: %w", absErr)
		}
		return storelock.Lock(ctx, storeAbs, filepath.Join(storeAbs, filepath.FromSlash(subtree)))
	}
	dir := filepath.Join(env.GetCachePath(), "oci-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	lockPath := filepath.Join(dir, digest.XXH3Multi([]byte(storeRoot), []byte(subtree)).Hex()+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock subtree %q: %w", subtree, err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
