package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/logger"
)

// TestAfterFixResetsLintOnChange covers the "file modified by fix" branch:
// a file that already passed lint must drop its lint mark and record the fix
// once its content changes.
func TestAfterFixResetsLintOnChange(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, map[string][]string{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	if err := c.AfterLint(file, "tool", true); err != nil {
		t.Fatalf("AfterLint() error = %v", err)
	}

	// Mutate the file, then record a fix → lint cache must reset.
	if err := os.WriteFile(file, []byte("package main // fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.AfterFix(file, "tool", true); err != nil {
		t.Fatalf("AfterFix() error = %v", err)
	}

	rel, err := filepath.Rel(projectPath, file)
	if err != nil {
		t.Fatal(err)
	}
	entry := c.data.Entries[rel]
	if len(entry.Lint) != 0 {
		t.Errorf("lint cache not reset after content change: %v", entry.Lint)
	}
	if len(entry.Fix) != 1 || entry.Fix[0] != "tool" {
		t.Errorf("fix not recorded after change: %v", entry.Fix)
	}
}

// TestAfterFixUnchangedFileAddsFix covers the "unchanged" branch, including the
// brand-new-entry path (no prior lint/fix) and the idempotent re-add.
func TestAfterFixUnchangedFileAddsFix(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, map[string][]string{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	// No prior entry → AfterFix creates one and records the tool.
	if err := c.AfterFix(file, "tool", true); err != nil {
		t.Fatalf("AfterFix() error = %v", err)
	}
	// Same tool again on an unchanged file is a no-op (no duplicate).
	if err := c.AfterFix(file, "tool", true); err != nil {
		t.Fatalf("AfterFix() second call error = %v", err)
	}

	rel, err := filepath.Rel(projectPath, file)
	if err != nil {
		t.Fatal(err)
	}
	entry := c.data.Entries[rel]
	if len(entry.Fix) != 1 || entry.Fix[0] != "tool" {
		t.Errorf("fix list = %v, want exactly [tool]", entry.Fix)
	}
}

// TestAfterDisabledToolIsNoOp covers the toolCacheEnabled=false early returns
// for both AfterLint and AfterFix.
func TestAfterDisabledToolIsNoOp(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, map[string][]string{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	if err := c.AfterLint(file, "tool", false); err != nil {
		t.Fatalf("AfterLint(disabled) error = %v", err)
	}
	if err := c.AfterFix(file, "tool", false); err != nil {
		t.Fatalf("AfterFix(disabled) error = %v", err)
	}
	if len(c.data.Entries) != 0 {
		t.Errorf("disabled cache should record nothing, got %d entries", len(c.data.Entries))
	}
}

// TestNilDataErrors covers the "cache data is nil" guard branches in Save,
// AfterFix, and markPassed (via AfterLint).
func TestNilDataErrors(t *testing.T) {
	c := &Cache{
		projectPath: t.TempDir(),
		logger:      logger.Logger,
	}

	if err := c.Save(); err == nil {
		t.Error("Save with nil data expected error, got nil")
	}
	if err := c.AfterFix("/some/file", "tool", true); err == nil {
		t.Error("AfterFix with nil data expected error, got nil")
	}
	if err := c.AfterLint("/some/file", "tool", true); err == nil {
		t.Error("AfterLint with nil data expected error, got nil")
	}
}

// TestDebounceSaveFlushesOnTimer covers debounceSave: MarkDirty schedules a
// delayed save that fires on its own (without an explicit Shutdown).
func TestDebounceSaveFlushesOnTimer(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, map[string][]string{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	t.Cleanup(c.Shutdown)

	if err := c.AfterLint(file, "tool", true); err != nil {
		t.Fatalf("AfterLint() error = %v", err)
	}
	// Remove any existing file so we can observe the debounced write.
	_ = os.Remove(c.path)

	c.MarkDirty() // schedules a 100ms debounced save
	c.MarkDirty() // coalesces — restarts the same timer

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(c.path); err == nil {
			return // debounced save fired
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("debounced save did not write the cache file within the deadline")
}
