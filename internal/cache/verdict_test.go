package cache

import (
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	c, err := NewCache(t.TempDir(), t.TempDir(), config.Config{}, nil, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return c
}

const testTTL = time.Hour

func TestVerdictRoundTrip(t *testing.T) {
	c := newTestCache(t)

	if !c.ShouldRunVerdict("k", "inputs", testTTL) {
		t.Error("an empty cache must not report a pass")
	}

	c.AfterVerdict("k", VerdictEntry{Tool: "tsc", Op: "lint", InputHash: "inputs", Members: 3})

	if c.ShouldRunVerdict("k", "inputs", testTTL) {
		t.Error("a recorded pass with identical inputs should skip the run")
	}
	// Any input change is a miss: that is the whole precondition.
	if !c.ShouldRunVerdict("k", "different", testTTL) {
		t.Error("changed inputs must miss")
	}
	if !c.ShouldRunVerdict("other", "inputs", testTTL) {
		t.Error("a different identity must miss")
	}
}

func TestVerdictTTL(t *testing.T) {
	c := newTestCache(t)
	c.AfterVerdict("k", VerdictEntry{InputHash: "inputs"})

	if !c.ShouldRunVerdict("k", "inputs", time.Nanosecond) {
		t.Error("an entry older than the TTL must miss")
	}
	// A zero TTL disables the mechanism outright.
	if !c.ShouldRunVerdict("k", "inputs", 0) {
		t.Error("ttl <= 0 must disable the verdict cache")
	}
}

// A backwards clock jump would otherwise leave an entry permanently fresh.
func TestVerdictFutureTimestampIsNotTrusted(t *testing.T) {
	c := newTestCache(t)
	c.mu.Lock()
	c.data.Verdicts["k"] = VerdictEntry{InputHash: "inputs", ValidatedAt: time.Now().Add(time.Hour)}
	c.mu.Unlock()

	if !c.ShouldRunVerdict("k", "inputs", time.Minute) {
		t.Error("a future timestamp must not count as fresh")
	}
}

func TestInvalidateVerdictsByTool(t *testing.T) {
	c := newTestCache(t)
	c.AfterVerdict("a", VerdictEntry{Tool: "tsc", InputHash: "x"})
	c.AfterVerdict("b", VerdictEntry{Tool: "eslint", InputHash: "y"})

	c.InvalidateVerdicts("tsc")

	if c.ShouldRunVerdict("b", "y", testTTL) {
		t.Error("an unrelated tool's verdict must survive")
	}
	if !c.ShouldRunVerdict("a", "x", testTTL) {
		t.Error("the invalidated tool's verdict must be gone")
	}
}

// Two processes share the file. A save must fold in the other's writes rather
// than overwriting them — and must never resurrect what the other invalidated.
func TestSaveMergesVerdictsFromDisk(t *testing.T) {
	dir, project := t.TempDir(), t.TempDir()

	first, err := NewCache(dir, project, config.Config{}, nil, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	first.AfterVerdict("from-first", VerdictEntry{Tool: "a", InputHash: "x"})
	if err := first.Save(); err != nil {
		t.Fatalf("first save: %v", err)
	}

	second, err := NewCache(dir, project, config.Config{}, nil, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	second.AfterVerdict("from-second", VerdictEntry{Tool: "b", InputHash: "y"})
	if err := second.Save(); err != nil {
		t.Fatalf("second save: %v", err)
	}

	reloaded, err := NewCache(dir, project, config.Config{}, nil, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	if reloaded.ShouldRunVerdict("from-first", "x", testTTL) {
		t.Error("the first process's verdict was lost")
	}
	if reloaded.ShouldRunVerdict("from-second", "y", testTTL) {
		t.Error("the second process's verdict was lost")
	}
}

// A file whose content diverges between two views cannot be reconciled: keeping
// either tool list would credit tools with content they never saw.
func TestSaveDropsFileEntriesWithConflictingContent(t *testing.T) {
	dir, project := t.TempDir(), t.TempDir()

	first, _ := NewCache(dir, project, config.Config{}, nil, nil, zap.NewNop())
	first.mu.Lock()
	first.data.Entries["a.go"] = FileEntry{ContentHash: "h1", Lint: []string{"vet"}}
	first.mu.Unlock()
	if err := first.Save(); err != nil {
		t.Fatalf("first save: %v", err)
	}

	second, _ := NewCache(dir, project, config.Config{}, nil, nil, zap.NewNop())
	second.mu.Lock()
	second.data.Entries["a.go"] = FileEntry{ContentHash: "h2", Lint: []string{"golangci"}}
	second.mu.Unlock()
	if err := second.Save(); err != nil {
		t.Fatalf("second save: %v", err)
	}

	reloaded, _ := NewCache(dir, project, config.Config{}, nil, nil, zap.NewNop())
	reloaded.mu.RLock()
	_, present := reloaded.data.Entries["a.go"]
	reloaded.mu.RUnlock()
	if present {
		t.Error("conflicting content hashes must drop the entry, not pick a winner")
	}
}

// Files written before unit caching existed have no Verdicts map; every lookup
// must miss rather than panic.
func TestNilVerdictsMapMisses(t *testing.T) {
	c := newTestCache(t)
	c.mu.Lock()
	c.data.Verdicts = nil
	c.mu.Unlock()

	if !c.ShouldRunVerdict("k", "inputs", testTTL) {
		t.Error("a nil verdict map must miss")
	}
	// And a write against it must not panic on a nil map.
	c.AfterVerdict("k", VerdictEntry{InputHash: "inputs"})
	if c.ShouldRunVerdict("k", "inputs", testTTL) {
		t.Error("the write should have created the map and stored the entry")
	}
}
