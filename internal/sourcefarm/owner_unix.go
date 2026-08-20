//go:build unix

package sourcefarm

import (
	"io/fs"
	"syscall"
)

// ownerUID reports the owning uid of a stat result. ok is false when the
// platform does not expose one.
func ownerUID(info fs.FileInfo) (uid int, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, false
	}
	return int(stat.Uid), true
}
