//go:build windows

package ocibundle

// lockSubtree is a no-op on Windows: the final placement is an atomic rename
// onto a content-addressed path, so the lock only ever prevented duplicated
// work between parallel invocations — acceptable to forgo here.
func lockSubtree(storeRoot, subtree string) (release func(), err error) {
	return func() {}, nil
}
