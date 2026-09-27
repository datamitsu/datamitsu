//go:build linux

package tooling

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

// groupAlive reports whether a process of group pgid still runs. A group of
// zombies answers kill(-pgid, 0) like a live one, and an orphan stays a zombie
// until init reaps it, which a container's init may never do; waiting on it
// would hold every stopped run for the whole grace.
func groupAlive(pgid int) bool {
	if syscall.Kill(-pgid, 0) != nil {
		return false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	want := []byte(strconv.Itoa(pgid))
	for _, e := range entries {
		if name := e.Name(); name[0] < '0' || name[0] > '9' {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// The command name before the fields may itself hold ") ".
		i := bytes.LastIndex(stat, []byte(") "))
		if i < 0 {
			continue
		}
		// The fields start with the state, the parent and the process group.
		fields := bytes.Fields(stat[i+2:])
		if len(fields) < 3 || !bytes.Equal(fields[2], want) {
			continue
		}
		if state := fields[0][0]; state != 'Z' && state != 'X' {
			return true
		}
	}
	return false
}
