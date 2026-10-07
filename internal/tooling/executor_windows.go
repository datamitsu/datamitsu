//go:build windows

package tooling

import (
	"os/exec"
	"time"
)

// killGroupAfterGrace has nothing to do on Windows, where a tool does not run
// in a process group of its own.
func killGroupAfterGrace(int, time.Time) {}

func setupProcessGroupCleanup(cmd *exec.Cmd) {
	// Windows doesn't support process groups the same way Unix does
	// Just use default behavior
}
