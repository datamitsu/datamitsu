package remotecfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveCached_MkdirError(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file, then try to save under a path that treats it as a
	// directory — MkdirAll must fail.
	fileAsDir := filepath.Join(dir, "blocker")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(fileAsDir, "sub", "config.ts")
	if err := SaveCached(path, "content"); err == nil {
		t.Error("SaveCached() expected error when parent path is a file, got nil")
	}
}

func TestSaveCached_LoadCached_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := CachedConfigPath(dir, "https://example.com/cfg.ts")

	if err := SaveCached(path, "export const x = 1;"); err != nil {
		t.Fatalf("SaveCached() error = %v", err)
	}
	got, err := LoadCached(path)
	if err != nil {
		t.Fatalf("LoadCached() error = %v", err)
	}
	if got != "export const x = 1;" {
		t.Errorf("LoadCached() = %q, want round-tripped content", got)
	}
}
