package runtimemanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	t.Run("copies content and mode", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		dst := filepath.Join(dir, "dst")
		if err := os.WriteFile(src, []byte("payload"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := copyFile(src, dst); err != nil {
			t.Fatalf("copyFile error = %v", err)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "payload" {
			t.Errorf("content = %q, want %q", string(data), "payload")
		}
		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o750 {
			t.Errorf("mode = %v, want 0750", info.Mode().Perm())
		}
		// Source must remain (copy, not move).
		if _, err := os.Stat(src); err != nil {
			t.Errorf("source missing after copy: %v", err)
		}
	})

	t.Run("missing source errors", func(t *testing.T) {
		dir := t.TempDir()
		if err := copyFile(filepath.Join(dir, "nope"), filepath.Join(dir, "dst")); err == nil {
			t.Error("expected error for missing source")
		}
	})

	t.Run("missing destination directory errors", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Destination dir does not exist → OpenFile fails.
		if err := copyFile(src, filepath.Join(dir, "missing-dir", "dst")); err == nil {
			t.Error("expected error writing into nonexistent directory")
		}
	})
}

func TestCopyDir(t *testing.T) {
	t.Run("copies nested files, dirs and symlinks", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		nested := filepath.Join(src, "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nested, "deep.txt"), []byte("deep"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Relative symlink so the copy stays valid in the new location.
		if err := os.Symlink("top.txt", filepath.Join(src, "link")); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}

		dst := filepath.Join(dir, "dst")
		if err := copyDir(src, dst); err != nil {
			t.Fatalf("copyDir error = %v", err)
		}

		top, err := os.ReadFile(filepath.Join(dst, "top.txt"))
		if err != nil || string(top) != "top" {
			t.Errorf("top.txt = %q, err = %v", string(top), err)
		}
		deep, err := os.ReadFile(filepath.Join(dst, "nested", "deep.txt"))
		if err != nil || string(deep) != "deep" {
			t.Errorf("nested/deep.txt = %q, err = %v", string(deep), err)
		}
		target, err := os.Readlink(filepath.Join(dst, "link"))
		if err != nil || target != "top.txt" {
			t.Errorf("link target = %q, err = %v", target, err)
		}
	})

	t.Run("missing source errors", func(t *testing.T) {
		dir := t.TempDir()
		if err := copyDir(filepath.Join(dir, "nope"), filepath.Join(dir, "dst")); err == nil {
			t.Error("expected error reading nonexistent source dir")
		}
	})
}
