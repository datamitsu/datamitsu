//go:build darwin || freebsd

package cache

import (
	"os"
	"syscall"
)

func statIdentity(fi os.FileInfo) FileIdentity {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return FileIdentity{}
	}
	return FileIdentity{
		Ino:            st.Ino,
		ChangeTimeNano: st.Ctimespec.Nano(),
		Known:          true,
	}
}
