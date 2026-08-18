package cache

import "time"

// VerdictEntry records that one operation over one unit passed while its inputs
// hashed to InputHash. Only passes are stored: a failure must reproduce so the
// user sees the diagnostics, and a cached failure withholds an error that may
// already be fixed.
//
// Identity lives in the map key and inputs in the value, so editing a file is a
// hit-and-mismatch that overwrites itself rather than orphaning an entry.
type VerdictEntry struct {
	Tool        string
	Op          string
	UnitDir     string // relative to the project root; "" is the root itself
	InputHash   string
	Members     int       // membership size, for reporting only
	ValidatedAt time.Time // TTL and prune
}

// ShouldRunVerdict reports whether an operation must execute. It returns false
// only for an exact match: same identity, same inputs, inside the TTL.
//
// ttl <= 0 disables the verdict cache, so every call runs.
func (c *Cache) ShouldRunVerdict(key, inputHash string, ttl time.Duration) bool {
	if ttl <= 0 || key == "" || inputHash == "" {
		return true
	}

	c.mu.RLock()
	entry, ok := c.data.Verdicts[key]
	c.mu.RUnlock()

	if !ok || entry.InputHash != inputHash {
		c.misses.Add(1)
		return true
	}
	// A timestamp in the future means a clock jump, not a fresh entry; treat it
	// as expired rather than trusting it indefinitely.
	age := time.Since(entry.ValidatedAt)
	if age < 0 || age > ttl {
		c.misses.Add(1)
		return true
	}

	c.hits.Add(1)
	return false
}

// AfterVerdict records a pass. Callers must only reach it when the run actually
// covered the unit — the planner decides that, never the executor.
func (c *Cache) AfterVerdict(key string, entry VerdictEntry) {
	if key == "" || entry.InputHash == "" {
		return
	}

	c.mu.Lock()
	if c.data.Verdicts == nil {
		c.data.Verdicts = make(map[string]VerdictEntry)
	}
	entry.ValidatedAt = time.Now()
	c.data.Verdicts[key] = entry
	c.mu.Unlock()

	c.MarkDirty()
}

// InvalidateVerdicts drops every stored verdict for a tool. A fix that rewrote
// files makes any earlier lint verdict for the same tool unsound, mirroring the
// per-file AfterFix behaviour.
func (c *Cache) InvalidateVerdicts(tool string) {
	c.mu.Lock()
	for key, entry := range c.data.Verdicts {
		if entry.Tool == tool {
			delete(c.data.Verdicts, key)
		}
	}
	c.mu.Unlock()

	c.MarkDirty()
}

// verdictPruneTTL is how long an unreferenced verdict survives on disk. It is
// deliberately generous: the read path enforces the real TTL, so this only stops
// the file growing without bound.
const verdictPruneTTL = 30 * 24 * time.Hour

// pruneVerdicts drops entries older than ttl. Called from Prune, which already
// runs at most daily.
func (c *Cache) pruneVerdicts(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	removed := 0
	for key, entry := range c.data.Verdicts {
		if time.Since(entry.ValidatedAt) > ttl {
			delete(c.data.Verdicts, key)
			removed++
		}
	}
	return removed
}
