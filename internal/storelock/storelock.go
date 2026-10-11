// Package storelock provides inter-process subtree locks over the datamitsu
// store. Lock files live under {cache} — never inside the store itself — so
// `store clear` cannot unlink a held lock's inode and let a second process
// lock a replacement inode underneath the first.
//
// The lock is keyed by (store root, subtree): OCI seeding and a prepare step
// placing the same subtree serialize through it, and a future `store gc`
// takes the same lock before removing anything.
package storelock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
)

// Lock blocks until the lock for (storeRoot, subtree) is acquired, the
// context is canceled, or locking fails. The returned release function drops
// the lock and is safe to call more than once.
//
// Waiting is cancelable: a caller whose context expires stops waiting rather
// than blocking indefinitely behind a slow holder (the blocker itself still
// runs to completion; the lock only guards concurrent placement).
func Lock(ctx context.Context, storeRoot, subtree string) (release func(), err error) {
	dir := filepath.Join(env.GetCachePath(), "store-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store lock directory: %w", err)
	}
	name := filepath.Join(dir, digest.XXH3Multi([]byte(storeRoot), []byte(subtree)).Hex()+".lock")
	return lockFile(ctx, name)
}
