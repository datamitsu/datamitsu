package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

// reopen builds a second Cache over the same files, which is what the next CLI
// invocation does. Assertions about "cleared" or "pruned" only mean anything
// against what survives on disk.
func reopen(t *testing.T, cacheDir, projectPath string) *Cache {
	t.Helper()
	c, err := NewCache(cacheDir, projectPath, config.Config{Tools: config.MapOfTools{}}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return c
}

func seededCache(t *testing.T) (c *Cache, cacheDir, projectPath, tracked string) {
	t.Helper()
	cacheDir, projectPath = t.TempDir(), t.TempDir()
	tracked = filepath.Join(projectPath, "a.ts")
	if err := os.WriteFile(tracked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	c = reopen(t, cacheDir, projectPath)
	if err := c.AfterLint(tracked, "eslint", true); err != nil {
		t.Fatalf("AfterLint: %v", err)
	}
	c.AfterVerdict("k", VerdictEntry{Tool: "tsc", Op: "lint", InputHash: "h"})
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return c, cacheDir, projectPath, tracked
}

// Clear emptied the maps and saved, but Save merges from disk first and Clear
// left no tombstones — so every entry it had just dropped was copied straight
// back in and rewritten. `datamitsu cache clear` reported success and cleared
// nothing.
func TestClearSurvivesReload(t *testing.T) {
	c, cacheDir, projectPath, _ := seededCache(t)

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	reloaded := reopen(t, cacheDir, projectPath)
	if n := len(reloaded.data.Entries); n != 0 {
		t.Errorf("%d file entries survived Clear on disk, want 0", n)
	}
	if n := len(reloaded.data.Verdicts); n != 0 {
		t.Errorf("%d verdicts survived Clear on disk, want 0", n)
	}
}

// Pruning has to leave a tombstone for the same reason a delete does: the merge
// can only add, so an entry dropped in memory and without a tombstone is restored
// from disk by the very save meant to persist its removal. Without this the
// documented 30-day pass never shrinks the file and the map grows without bound.
func TestPruneVerdictsSurvivesReload(t *testing.T) {
	cacheDir, projectPath := t.TempDir(), t.TempDir()

	// Age the entry before the first save, so what lands on disk is itself
	// expired. Backdating after a save would not survive: the merge keeps the
	// newer of the two timestamps and would restore the fresh disk copy.
	seed := reopen(t, cacheDir, projectPath)
	seed.AfterVerdict("k", VerdictEntry{Tool: "tsc", Op: "lint", InputHash: "h"})
	stale := seed.data.Verdicts["k"]
	stale.ValidatedAt = time.Now().Add(-2 * verdictPruneTTL)
	seed.data.Verdicts["k"] = stale
	if err := seed.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c := reopen(t, cacheDir, projectPath)
	if _, ok := c.data.Verdicts["k"]; !ok {
		t.Fatal("precondition: the expired verdict should be on disk to begin with")
	}
	c.Prune()
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded := reopen(t, cacheDir, projectPath)
	if _, ok := reloaded.data.Verdicts["k"]; ok {
		t.Error("the expired verdict came back on reload; pruning does not bound the file")
	}
}

// Prune already tombstones file entries; keep that working alongside the
// verdict fix.
func TestPruneRemovesVanishedFilesOnDisk(t *testing.T) {
	c, cacheDir, projectPath, tracked := seededCache(t)

	if err := os.Remove(tracked); err != nil {
		t.Fatal(err)
	}
	c.Prune()
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded := reopen(t, cacheDir, projectPath)
	if n := len(reloaded.data.Entries); n != 0 {
		t.Errorf("%d entries for deleted files survived on disk, want 0", n)
	}
}
