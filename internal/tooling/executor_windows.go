//go:build windows

package tooling

import (
	"os"
	"os/exec"
)

// endedOnItsOwn cannot tell a killed process from one that exited on Windows,
// so the delivered stop decides.
func endedOnItsOwn(*os.ProcessState) bool {
	return false
}

func setupProcessGroupCleanup(cmd *exec.Cmd) {
	// Windows doesn't support process groups the same way Unix does
	// Just use default behavior
}
