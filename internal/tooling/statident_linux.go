//go:build linux

package tooling

import (
	"os"
	"syscall"
)

func statIdent(fi os.FileInfo) fileIdent {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdent{}
	}
	return fileIdent{
		ino:       st.Ino,
		ctimeNano: st.Ctim.Nano(),
		known:     true,
	}
}
