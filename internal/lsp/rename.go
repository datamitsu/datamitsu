package lsp

import (
	"os"
	"time"
)

// renameRetryLimit bounds how long a rename waits for another process to let
// go of the file it replaces.
const renameRetryLimit = 500 * time.Millisecond

// renameOver renames src over dst. On Windows that fails while another process
// holds dst open without FILE_SHARE_DELETE — a Go or C runtime reader, a virus
// scanner — so a transient failure is retried briefly, as the go command
// itself does.
func renameOver(src, dst string) error {
	return retryTransient(func() error { return os.Rename(src, dst) }, transientRenameError, renameRetryLimit)
}

func retryTransient(op func() error, transient func(error) bool, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	delay := time.Millisecond
	for {
		err := op()
		if err == nil || !transient(err) || time.Now().Add(delay).After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(2*delay, 100*time.Millisecond)
	}
}
