package cache

import (
	"testing"
	"time"
)

// An empty key or input hash means the caller could not describe the question.
// Storing it anyway would create an entry nothing can ever match, or worse, one
// that every other empty-keyed call collides with.
func TestAfterVerdictRejectsIncompleteEntries(t *testing.T) {
	c := newVerdictTestCache()

	c.AfterVerdict("", VerdictEntry{InputHash: "h"})
	c.AfterVerdict("k", VerdictEntry{InputHash: ""})

	if len(c.data.Verdicts) != 0 {
		t.Errorf("stored %d verdict(s), want none", len(c.data.Verdicts))
	}
}

func TestDeleteVerdictIgnoresAnEmptyKey(t *testing.T) {
	c := newVerdictTestCache()
	c.DeleteVerdict("")

	if len(c.deletedVerdicts) != 0 {
		t.Errorf("recorded %d tombstone(s) for an empty key, want none", len(c.deletedVerdicts))
	}
}

// A tombstone is what makes a delete survive the merge with what is already on
// disk: the merge can only add, so without one the entry comes straight back.
func TestDeleteVerdictLeavesATombstone(t *testing.T) {
	c := newVerdictTestCache()
	c.AfterVerdict("k", VerdictEntry{InputHash: "h"})
	c.DeleteVerdict("k")

	if _, ok := c.data.Verdicts["k"]; ok {
		t.Error("the verdict survived the delete")
	}
	if _, ok := c.deletedVerdicts["k"]; !ok {
		t.Error("no tombstone; the next merge resurrects the deleted verdict")
	}
}

func TestShouldRunVerdict(t *testing.T) {
	fresh := func() *Cache {
		c := newVerdictTestCache()
		c.AfterVerdict("k", VerdictEntry{InputHash: "h"})
		return c
	}

	tests := []struct {
		name    string
		key     string
		inputs  string
		ttl     time.Duration
		wantRun bool
	}{
		{"exact match inside the TTL", "k", "h", time.Hour, false},
		{"different inputs", "k", "other", time.Hour, true},
		{"unknown key", "missing", "h", time.Hour, true},
		{"ttl disables the cache", "k", "h", 0, true},
		{"negative ttl disables the cache", "k", "h", -time.Second, true},
		{"empty key", "", "h", time.Hour, true},
		{"empty inputs", "k", "", time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fresh().ShouldRunVerdict(tt.key, tt.inputs, tt.ttl); got != tt.wantRun {
				t.Errorf("ShouldRunVerdict = %v, want %v", got, tt.wantRun)
			}
		})
	}

	t.Run("expired entry", func(t *testing.T) {
		c := newVerdictTestCache()
		c.AfterVerdict("k", VerdictEntry{InputHash: "h"})
		entry := c.data.Verdicts["k"]
		entry.ValidatedAt = time.Now().Add(-2 * time.Hour)
		c.data.Verdicts["k"] = entry

		if !c.ShouldRunVerdict("k", "h", time.Hour) {
			t.Error("an expired verdict was trusted")
		}
	})

	// A timestamp in the future is a clock jump, not a fresh entry; trusting it
	// would pin the verdict until the clock caught up.
	t.Run("timestamp in the future", func(t *testing.T) {
		c := newVerdictTestCache()
		c.AfterVerdict("k", VerdictEntry{InputHash: "h"})
		entry := c.data.Verdicts["k"]
		entry.ValidatedAt = time.Now().Add(time.Hour)
		c.data.Verdicts["k"] = entry

		if !c.ShouldRunVerdict("k", "h", time.Minute) {
			t.Error("a verdict dated in the future was trusted")
		}
	})
}

func TestPruneVerdicts(t *testing.T) {
	build := func() *Cache {
		c := newVerdictTestCache()
		c.AfterVerdict("old", VerdictEntry{InputHash: "h"})
		c.AfterVerdict("new", VerdictEntry{InputHash: "h"})
		stale := c.data.Verdicts["old"]
		stale.ValidatedAt = time.Now().Add(-48 * time.Hour)
		c.data.Verdicts["old"] = stale
		return c
	}

	t.Run("drops entries past the ttl", func(t *testing.T) {
		c := build()
		if removed := c.pruneVerdicts(24 * time.Hour); removed != 1 {
			t.Errorf("pruneVerdicts removed %d, want 1", removed)
		}
		if _, ok := c.data.Verdicts["old"]; ok {
			t.Error("the stale verdict survived the prune")
		}
		if _, ok := c.data.Verdicts["new"]; !ok {
			t.Error("the fresh verdict was pruned")
		}
	})

	t.Run("a non-positive ttl prunes nothing", func(t *testing.T) {
		c := build()
		if removed := c.pruneVerdicts(0); removed != 0 {
			t.Errorf("pruneVerdicts(0) removed %d, want 0", removed)
		}
		if len(c.data.Verdicts) != 2 {
			t.Errorf("kept %d verdicts, want 2", len(c.data.Verdicts))
		}
	})
}

// newVerdictTestCache is a Cache with just enough state for the verdict map;
// the zero value has a nil *File and panics on first write.
func newVerdictTestCache() *Cache {
	return &Cache{data: &File{Verdicts: map[string]VerdictEntry{}}}
}
