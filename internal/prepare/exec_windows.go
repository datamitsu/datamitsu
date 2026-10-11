//go:build windows

package prepare

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientRenameError reports rename failures worth retrying: sharing
// violations from antivirus or indexer scans that hold a freshly written
// file open for a moment.
func isTransientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// stillActive is GetExitCodeProcess's sentinel for a running process.
const stillActive = 259

// processProbe reports whether a process with the given PID exists.
func processProbe(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			// The PID is outside the valid range: no such process.
			return errors.New("no such process")
		}
		return err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return err
	}
	if code == stillActive {
		return nil
	}
	// The handle exists but the process has exited: the PID is effectively
	// free, so report absence.
	return errors.New("process has exited")
}
