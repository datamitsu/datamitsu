package prepare

import (
	"context"

	"github.com/datamitsu/datamitsu/internal/storelock"
)

// storeLock is the inter-process lock seam.
func storeLock(ctx context.Context, storeRoot, subtree string) (func(), error) {
	return storelock.Lock(ctx, storeRoot, subtree)
}

// processAlive reports whether a process with the given PID exists.
func processAlive(pid int) bool {
	return processProbe(pid) == nil
}
