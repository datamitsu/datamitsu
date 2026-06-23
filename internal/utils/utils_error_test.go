package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDir_Error(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a regular file, then ask for a directory beneath it: MkdirAll
	// cannot create a child of a non-directory, so EnsureDir must error.
	blocker := filepath.Join(tmpDir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := EnsureDir(filepath.Join(blocker, "child")); err == nil {
		t.Error("EnsureDir() expected error when parent is a file, got nil")
	}
}

func TestWriteFile_Error(t *testing.T) {
	tmpDir := t.TempDir()
	blocker := filepath.Join(tmpDir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Parent dir cannot be created because a file occupies the path.
	if err := WriteFile(filepath.Join(blocker, "sub", "out"), []byte("data")); err == nil {
		t.Error("WriteFile() expected error when parent dir cannot be created, got nil")
	}
}

func TestReadFileIfExists_Error(t *testing.T) {
	// A directory exists but cannot be read as a file → os.ReadFile errors.
	tmpDir := t.TempDir()
	if _, err := ReadFileIfExists(tmpDir); err == nil {
		t.Error("ReadFileIfExists() expected error reading a directory, got nil")
	}
}

func TestRenameReplace_Error(t *testing.T) {
	tmpDir := t.TempDir()
	// Source does not exist → os.Rename fails; on non-Windows this surfaces
	// directly as an error.
	src := filepath.Join(tmpDir, "does-not-exist")
	dst := filepath.Join(tmpDir, "dst")
	if err := RenameReplace(src, dst); err == nil {
		t.Error("RenameReplace() expected error for missing src, got nil")
	}
}
