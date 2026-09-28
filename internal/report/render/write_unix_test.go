//go:build unix

package render

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

// A report gets the mode a file created in its place would: the umask
// decides, and a report it replaces keeps its own permissions.
func TestTargetMode(t *testing.T) {
	write := func(t *testing.T, path string) os.FileMode {
		t.Helper()
		target := Open(Spec{Format: "json", Path: path}, nil)
		if err := target.Write(&report.Run{Schema: report.SchemaVersion}); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	withUmask := func(t *testing.T, mask int) {
		t.Helper()
		prev := syscall.Umask(mask)
		t.Cleanup(func() { syscall.Umask(prev) })
	}

	t.Run("umask 077", func(t *testing.T) {
		withUmask(t, 0o077)
		if mode := write(t, filepath.Join(t.TempDir(), "run.json")); mode != 0o600 {
			t.Errorf("mode = %o, want 600", mode)
		}
	})
	t.Run("umask 022", func(t *testing.T) {
		withUmask(t, 0o022)
		if mode := write(t, filepath.Join(t.TempDir(), "run.json")); mode != 0o644 {
			t.Errorf("mode = %o, want 644", mode)
		}
	})
	t.Run("an existing private report stays private", func(t *testing.T) {
		withUmask(t, 0o022)
		path := filepath.Join(t.TempDir(), "run.json")
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if mode := write(t, path); mode != 0o600 {
			t.Errorf("mode = %o, want the existing 600", mode)
		}
	})
}
