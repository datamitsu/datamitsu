//go:build windows

package ocibundle

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/storelock"
)

// lockSubtree is a no-op on Windows for ordinary subtrees: the final
// placement is an atomic rename onto a content-addressed path, so the lock
// only ever prevented duplicated work between parallel invocations —
// acceptable to forgo here.
//
// A PREPARED subtree still locks through internal/storelock (the lock the
// prepare engine holds on Windows too): a seed moving a markerless remnant
// aside and a run placing a fresh state must serialize.
func lockSubtree(ctx context.Context, storeRoot, subtree string) (release func(), err error) {
	if strings.HasPrefix(subtree, preparedSubtreeRoot) {
		storeAbs, absErr := filepath.Abs(storeRoot)
		if absErr != nil {
			return nil, fmt.Errorf("absolutize store root: %w", absErr)
		}
		return storelock.Lock(ctx, storeAbs, filepath.Join(storeAbs, filepath.FromSlash(subtree)))
	}
	return func() {}, nil
}
