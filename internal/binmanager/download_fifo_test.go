//go:build !windows

package binmanager

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestFileScheme_RejectsAFifo is the half of the regular-file guard that needs
// mkfifo, so it is unix-only (Windows has no syscall.Mkfifo, and the test binary
// would not compile there). Opening a FIFO with no writer blocks indefinitely,
// which is why copyLocalSource opens with O_NONBLOCK and decides on the fstat of
// the resulting descriptor — this test fails by timing out if that regresses.
func TestFileScheme_RejectsAFifo(t *testing.T) {
	withLocalArtifactsBuild(t)

	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create fifo here: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := copyLocalSource("file://"+fifo, t.TempDir())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a fifo must be rejected")
		}
		if !isPermanent(err) {
			t.Error("rejection must be permanent")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("copyLocalSource blocked on a fifo instead of rejecting it")
	}
}
