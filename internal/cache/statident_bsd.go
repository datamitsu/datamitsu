//go:build darwin || freebsd

package cache

import (
	"os"
	"syscall"
)

func statIdentity(fi os.FileInfo) FileIdentity {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return FileIdentity{Ino: 0, ChangeTimeNano: 0, Known: false}
	}
	return FileIdentity{
		Ino:            st.Ino,
		ChangeTimeNano: st.Ctimespec.Nano(),
		Known:          true,
	}
}
