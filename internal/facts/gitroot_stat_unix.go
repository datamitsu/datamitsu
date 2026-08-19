//go:build unix

package facts

import (
	"os"
	"syscall"
)

// statIdentity reports the owning user and the filesystem device of path.
//
// Both are what git itself looks at during repository discovery: the device to
// stop climbing at a mount boundary, the owner to refuse a repository whose
// working tree belongs to somebody else.
func statIdentity(path string) (dirIdentity, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return dirIdentity{}, false
	}

	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return dirIdentity{}, false
	}

	// st.Dev is signed on some platforms and unsigned on others. The value is
	// only ever compared with another one produced here, so the conversion just
	// has to be consistent.
	return dirIdentity{uid: uint64(st.Uid), device: uint64(st.Dev)}, true //nolint:gosec // identity, never arithmetic
}
