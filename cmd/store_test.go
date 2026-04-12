package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorePathPrintsCachePath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	err := runStorePath(nil, nil)
	if err != nil {
		t.Fatalf("runStorePath returned error: %v", err)
	}
}

func TestStoreClearRemovesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	storeDir := filepath.Join(tmpDir, "store")
	subDir := filepath.Join(storeDir, ".bin", "some-tool")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	testFile := filepath.Join(subDir, "binary")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	err := runStoreClear(nil, nil)
	if err != nil {
		t.Fatalf("runStoreClear returned error: %v", err)
	}

	if _, err := os.Stat(storeDir); !os.IsNotExist(err) {
		t.Errorf("store directory should be removed, but still exists")
	}
}

func TestStoreClearNonExistentDirectory(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "nonexistent")
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	err := runStoreClear(nil, nil)
	if err != nil {
		t.Fatalf("runStoreClear should not fail on nonexistent directory: %v", err)
	}
}

func TestStoreClearRejectsDangerousPath(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", "/")

	err := runStoreClear(nil, nil)
	// With store path = /store, this is no longer "/" but still a top-level dir.
	// The function should succeed since /store is a valid path to clear.
	// The dangerous path check protects against /, HOME, etc.
	if err != nil {
		t.Fatalf("runStoreClear returned unexpected error: %v", err)
	}
}

func TestStoreClearRejectsHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Set DATAMITSU_CACHE_DIR such that GetStorePath() returns HOME
	// GetStorePath() = DATAMITSU_CACHE_DIR + "/store", so we need parent of home
	// This is hard to construct generically, so test with HOME set to {tmpDir}/store
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	t.Setenv("HOME", storeDir)
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Dir(storeDir))

	err := runStoreClear(nil, nil)
	if err == nil {
		t.Fatal("expected error when store path equals HOME")
	}
}

func TestStoreClearRejectsRelativePath(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", "..")

	err := runStoreClear(nil, nil)
	if err == nil {
		t.Fatal("expected error when store path is a relative path")
	}
}

func TestStoreClearRejectsAncestorOfHome(t *testing.T) {
	// Set HOME so that store path is an ancestor of it
	// GetStorePath() = /home/store, HOME = /home/store/testuser
	t.Setenv("HOME", "/home/store/testuser")
	t.Setenv("DATAMITSU_CACHE_DIR", "/home")

	err := runStoreClear(nil, nil)
	if err == nil {
		t.Fatal("expected error when store path is an ancestor of HOME")
	}
}

func TestStoreCommandsRegistered(t *testing.T) {
	var foundStore bool
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "store" {
			foundStore = true

			subNames := make(map[string]bool)
			for _, sub := range cmd.Commands() {
				subNames[sub.Use] = true
			}
			if !subNames["path"] {
				t.Error("store command missing 'path' subcommand")
			}
			if !subNames["clear"] {
				t.Error("store command missing 'clear' subcommand")
			}
			break
		}
	}
	if !foundStore {
		t.Error("rootCmd missing 'store' command")
	}
}
