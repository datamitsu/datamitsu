//go:build !windows

package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestTakeNeverOpensAPipe: a tracked file replaced by a named pipe is recorded
// by its type, never opened — opening a pipe waits for a writer.
func TestTakeNeverOpensAPipe(t *testing.T) {
	r := committed(t)
	path := filepath.Join(r.dir, "clean.txt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Take(context.Background(), r.dir, Environ())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the snapshot blocked on a named pipe")
	}
}
