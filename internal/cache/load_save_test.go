package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/logger"
)

func newProject(t *testing.T) (cacheDir, projectPath, file string) {
	t.Helper()
	cacheDir = t.TempDir()
	projectPath = filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(projectPath, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cacheDir, projectPath, file
}

func TestShouldRunCacheHitAndChange(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	// Disabled cache → always run.
	if !c.ShouldRun(file, "tool", OperationLint, false) {
		t.Error("disabled tool cache should always run")
	}

	// First check → miss → should run.
	if !c.ShouldRun(file, "tool", OperationLint, true) {
		t.Error("uncached file should run")
	}

	// Record success → subsequent check is a hit → skip.
	if err := c.AfterLint(file, "tool", true); err != nil {
		t.Fatalf("AfterLint() error = %v", err)
	}
	if c.ShouldRun(file, "tool", OperationLint, true) {
		t.Error("passed file should be skipped (cache hit)")
	}

	// A different tool on the same file is still a miss.
	if !c.ShouldRun(file, "other", OperationLint, true) {
		t.Error("untracked tool should run")
	}

	// Mutating the file invalidates the hit.
	if err := os.WriteFile(file, []byte("package main // changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !c.ShouldRun(file, "tool", OperationLint, true) {
		t.Error("changed file should run again")
	}

	// A nonexistent file cannot be hashed → run.
	if !c.ShouldRun(filepath.Join(projectPath, "missing.go"), "tool", OperationLint, true) {
		t.Error("missing file should run")
	}
}

func TestLoadRoundTripAndInvalidation(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)

	c, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	if err := c.AfterLint(file, "tool", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reopen with the SAME inputs → Load reads the persisted entry → hit.
	c2, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("reopen NewCache() error = %v", err)
	}
	if c2.ShouldRun(file, "tool", OperationLint, true) {
		t.Error("persisted hit not restored after Load")
	}

	// Reopen with a DIFFERENT selected-tools set → invalidation key mismatch →
	// the cache resets and the previously-passed file runs again.
	c3, err := NewCache(cacheDir, projectPath, config.Config{}, []string{"other-tool"}, logger.Logger)
	if err != nil {
		t.Fatalf("reopen-with-tools NewCache() error = %v", err)
	}
	if !c3.ShouldRun(file, "tool", OperationLint, true) {
		t.Error("key mismatch should reset cache and force a run")
	}
}

func TestLoadCorruptFileFallsBack(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)

	// Pre-create a garbage cache file where NewCache expects it.
	projectsDir := filepath.Join(cacheDir, "projects")
	// NewCache computes the hashed project dir; create the cache first so we can
	// find the path, then clobber it and reopen.
	c, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	if err := os.WriteFile(c.path, []byte("not-msgpack-garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = projectsDir

	// Reopen → Load fails to decode → NewCache warns and starts fresh (no error).
	c2, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() over corrupt file should not error, got %v", err)
	}
	if c2.data == nil || c2.data.Entries == nil {
		t.Error("corrupt-file fallback should produce an empty entry map")
	}
	// Fresh cache → file runs.
	if !c2.ShouldRun(file, "tool", OperationLint, true) {
		t.Error("fresh cache should run the file")
	}
}

// invalidateOn no longer participates in the global key: it was resolved against
// the git root and so never saw a nested config, and a global key is the wrong
// shape for it anyway — one nested edit would reset the whole file for every
// tool. It is a per-unit guard in the verdict cache now, covered by
// tooling.TestVerdictInputsNoticeAConfigEditNoGlobMatches.
