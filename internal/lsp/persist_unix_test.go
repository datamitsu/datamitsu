//go:build unix

package lsp

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A buffer whose file does not exist yet is created under the umask, as any
// file the user creates is: a credentials file recreated on save stays private.
func TestPersistBufferCreatesUnderTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	path := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := persistBuffer(path, []byte("token: x")); err != nil {
		t.Fatalf("persistBuffer: %v", err)
	}
	if got := readFile(t, path); got != "token: x" {
		t.Errorf("content = %q", got)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
}
