//go:build linux

package installprocess

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

func groupAlive(pgid int) bool {
	if syscall.Kill(-pgid, 0) != nil {
		return false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	want := []byte(strconv.Itoa(pgid))
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		stat, err := os.ReadFile("/proc/" + name + "/stat")
		if err != nil {
			continue
		}
		_, after, ok := bytes.CutLast(stat, []byte(") "))
		if !ok {
			continue
		}
		fields := bytes.Fields(after)
		if len(fields) >= 3 && bytes.Equal(fields[2], want) && fields[0][0] != 'Z' && fields[0][0] != 'X' {
			return true
		}
	}
	return false
}
