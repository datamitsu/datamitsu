package managedconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func TestCreateDatamitsuTypeDefinitions_MkdirGitRootError(t *testing.T) {
	dir := t.TempDir()
	// Make gitRoot resolve under an existing regular file so MkdirAll fails.
	fileAsDir := filepath.Join(dir, "blocker")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRoot := filepath.Join(fileAsDir, "repo")

	if err := CreateDatamitsuTypeDefinitions(gitRoot, false); err == nil {
		t.Error("CreateDatamitsuTypeDefinitions() expected error when git root is under a file, got nil")
	}
}

func TestCreateDatamitsuLinks_MkdirGitRootError(t *testing.T) {
	dir := t.TempDir()
	fileAsDir := filepath.Join(dir, "blocker")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRoot := filepath.Join(fileAsDir, "repo")

	installRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(installRoot, "bin"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	apps := binmanager.MapOfApps{
		"tool": {Links: map[string]string{"bin": "bin"}},
	}
	resolver := &mockResolver{paths: map[string]string{"tool": installRoot}}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Error("CreateDatamitsuLinks() expected error when git root is under a file, got nil")
	}
}

func TestCreateDatamitsuTypeDefinitions_CreatesFiles(t *testing.T) {
	gitRoot := filepath.Join(t.TempDir(), "repo")
	if err := CreateDatamitsuTypeDefinitions(gitRoot, false); err != nil {
		t.Fatalf("CreateDatamitsuTypeDefinitions() error = %v", err)
	}
	for _, f := range []string{".gitignore", "datamitsu.config.d.ts"} {
		if _, err := os.Stat(filepath.Join(gitRoot, ".datamitsu", f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
}
