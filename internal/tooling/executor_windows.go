//go:build windows

package tooling

import (
	"os/exec"
)

func setupProcessGroupCleanup(cmd *exec.Cmd) {
	// Windows doesn't support process groups the same way Unix does
	// Just use default behavior
}
