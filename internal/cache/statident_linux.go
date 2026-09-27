//go:build linux

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
		ChangeTimeNano: st.Ctim.Nano(),
		Known:          true,
	}
}
