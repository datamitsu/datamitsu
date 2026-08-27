package tooling

import (
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/trace"
)

// Memo counters. A hit is a file whose bytes a second tool did not have to read;
// a miss is one this process hashed for the first time, or one whose stat could
// not clear the entry it found.
var (
	cntMemoHit  = trace.NewCounter("cache.verdict_hash_memo_hits")
	cntMemoMiss = trace.NewCounter("cache.verdict_hash_memo_misses")
)

// hashMemo memoizes "bytes of this path -> content hash" for the lifetime of one
// process. A file inside a package is an input to every per-project tool
// planning a task there, and its content does not depend on which tool is
// asking; without the memo the same bytes were read four to six times per run.
//
// It caches a pure function behind a validity check, not an assumption: an entry
// is only handed out when a fresh stat still reports the size and modification
// time the hash was taken under, *and* that modification time is at least
// mtimeGranularity in the past — a file written inside the current tick could be
// rewritten again at the same length and still show the same stat, which is the
// one write a stat comparison cannot see.
//
// The post-run probe in recordVerdict must never consult it. That probe exists
// to notice inputs that moved while the tool ran, and answering it from a cache
// populated by the pre-run pass would make it compare a value against itself.
type hashMemo struct {
	mu sync.RWMutex
	m  map[string]memoEntry
}

type memoEntry struct {
	hash string
	size int64
	mod  time.Time
}

func newHashMemo() *hashMemo { return &hashMemo{m: make(map[string]memoEntry)} }

// contentMemo is the process-scoped memo the pre-run verdict pass shares across
// every task of a run.
var contentMemo = newHashMemo()

// lookup returns the memoized hash for a path whose current stat is (size, mod),
// as observed at now. Every uncertain answer is a miss.
func (h *hashMemo) lookup(path string, size int64, mod, now time.Time) (string, bool) {
	if h == nil {
		return "", false
	}
	h.mu.RLock()
	e, ok := h.m[path]
	h.mu.RUnlock()
	if !ok || e.size != size || !e.mod.Equal(mod) {
		cntMemoMiss.Add(1)
		return "", false
	}
	if mod.After(now.Add(-mtimeGranularity)) {
		cntMemoMiss.Add(1)
		return "", false
	}
	cntMemoHit.Add(1)
	return e.hash, true
}

// store records the hash of the bytes read from a handle whose stat was
// (size, mod). Callers pass the stat of the open file description they hashed,
// never a later stat of the path.
func (h *hashMemo) store(path, hash string, size int64, mod time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.m == nil {
		h.m = make(map[string]memoEntry)
	}
	h.m[path] = memoEntry{hash: hash, size: size, mod: mod}
	h.mu.Unlock()
}
