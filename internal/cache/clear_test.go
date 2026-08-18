package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"
)

func TestPathEqualFold(t *testing.T) {
	if !pathEqualFold("/Foo/Bar", "/foo/bar") {
		t.Error("pathEqualFold should be case-insensitive")
	}
	if pathEqualFold("/foo", "/bar") {
		t.Error("pathEqualFold should distinguish different paths")
	}
}

func TestValidateCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"valid absolute path", filepath.Join(t.TempDir(), "datamitsu", "cache"), false},
		{"empty path", "", true},
		{"dot path", ".", true},
		{"relative path", "relative/cache", true},
		{"root", "/", true},
		{"home dir itself", home, true},
		{"ancestor of home", filepath.Dir(home), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCacheDir(tt.dir)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCacheDir(%q) error = %v, wantErr %v", tt.dir, err, tt.wantErr)
			}
		})
	}
}

func TestClearAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cacheDir := filepath.Join(t.TempDir(), "datamitsu", "cache")
	projectsDir := filepath.Join(cacheDir, "projects")
	sentinel := filepath.Join(projectsDir, "abc123", "x")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ClearAll(cacheDir); err != nil {
		t.Fatalf("ClearAll() error = %v", err)
	}
	if _, err := os.Stat(projectsDir); !os.IsNotExist(err) {
		t.Error("projects dir should have been removed")
	}

	// Idempotent: clearing again (now missing) is not an error.
	if err := ClearAll(cacheDir); err != nil {
		t.Errorf("ClearAll() second call error = %v", err)
	}

	// Dangerous path is refused.
	if err := ClearAll("/"); err == nil {
		t.Error("ClearAll(\"/\") expected error, got nil")
	}
}

func TestClearProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cacheDir := filepath.Join(t.TempDir(), "datamitsu", "cache")
	projectPath := filepath.Join(t.TempDir(), "myproject")
	projectHash := env.HashProjectPath(projectPath)
	projectDir := filepath.Join(cacheDir, "projects", projectHash)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ClearProject(cacheDir, projectPath); err != nil {
		t.Fatalf("ClearProject() error = %v", err)
	}
	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Error("project dir should have been removed")
	}

	if err := ClearProject("relative", projectPath); err == nil {
		t.Error("ClearProject with invalid cache dir expected error, got nil")
	}
}

func TestCacheClearAndPrune(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(projectPath, "a.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := NewCache(tmpDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	// Record a pass, then Prune must keep it (file still exists).
	if err := c.AfterLint(file, "tool", true); err != nil {
		t.Fatalf("AfterLint() error = %v", err)
	}
	c.Prune()
	if len(c.data.Entries) != 1 {
		t.Errorf("Prune removed a live entry: entries=%d", len(c.data.Entries))
	}

	// Delete the file → Prune drops the stale entry.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	c.Prune()
	if len(c.data.Entries) != 0 {
		t.Errorf("Prune did not drop stale entry: entries=%d", len(c.data.Entries))
	}

	// Clear resets everything and persists.
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if len(c.data.Entries) != 0 {
		t.Errorf("Clear left entries: %d", len(c.data.Entries))
	}
}

func TestCacheMarkDirtyAndShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := NewCache(tmpDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	c.MarkDirty() // schedules a debounced save
	c.Shutdown()  // flushes and stops the timer
	c.Shutdown()  // idempotent (shutdownOnce) — must not panic

	if _, err := os.Stat(c.path); err != nil {
		t.Errorf("cache file not written after Shutdown: %v", err)
	}
}
