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

	return dirIdentity{uid: widenID(st.Uid), device: widenID(st.Dev)}, true
}

// widenID widens a syscall.Stat_t identity field to uint64.
//
// The field types are platform-dependent — st.Dev is int32 on darwin and
// openbsd but uint64 on linux and freebsd — so a plain uint64(...) is
// load-bearing on one platform and a no-op on another, where unconvert reports
// it. Suppressing that with a //nolint only moves the problem, because the
// directive is then unused on the other platform and nolintlint reports *that*.
// Converting from a type parameter is the one expression that is correct, and
// lint-clean, on every unix.
//
// The values are only ever compared with others produced here, so reinterpreting
// the sign of a negative device number is harmless — it only has to be
// consistent.
func widenID[T int32 | int64 | uint32 | uint64](v T) uint64 {
	return uint64(v)
}
