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
// time the hash was taken under, *and* that modification time was already at
// least mtimeGranularity old when the bytes were read — a file written inside
// the tick the read landed in could be rewritten again at the same length and
// still show the same stat, which is the one write a stat comparison cannot see.
//
// The tick guard is anchored to the read, not to the lookup. Waiting does not
// make an entry taken inside its own tick trustworthy: the ambiguous write it
// cannot rule out already happened, so such an entry is never handed out at all.
//
// The post-run probe in recordVerdict must never consult it. That probe exists
// to notice inputs that moved while the tool ran, and answering it from a cache
// populated by the pre-run pass would make it compare a value against itself.
type hashMemo struct {
	mu sync.RWMutex
	m  map[string]memoEntry
}

// memoMode says how one hashing pass may use the memo.
type memoMode int

const (
	// memoShared looks entries up and stores what it read: the pre-run pass,
	// where the hash one tool computed is the hash the next tool needs.
	memoShared memoMode = iota
	// memoRewrite never looks an entry up, but still replaces every entry it
	// reads. It is the pass whose whole purpose is to notice a write the memo
	// could not — a fixer rewriting the bytes it was just handed — so consulting
	// the memo would compare a value against itself. Overwriting matters just as
	// much: leaving the pre-fix entries behind would hand them to the next task
	// in the same process, which is how `check` runs lint after fix.
	memoRewrite
)

type memoEntry struct {
	hash  string
	size  int64
	mod   time.Time
	ident fileIdent
	// taken is the instant just before the bytes were read, which is what the
	// modification time has to be old relative to.
	taken time.Time
}

// maxMemoEntries bounds the memo. A CLI run drops the whole map on exit, but a
// `datamitsu lsp` server keeps this process alive for a session and would
// otherwise retain one entry per path it ever hashed. The memo caches a pure
// function, so dropping it costs a re-read and nothing else; the cap is set well
// above the working set of a single run so a run never pays that.
const maxMemoEntries = 200_000

func newHashMemo() *hashMemo { return &hashMemo{m: make(map[string]memoEntry)} }

// contentMemo is the process-scoped memo the pre-run verdict pass shares across
// every task of a run.
var contentMemo = newHashMemo()

// lookup returns the memoized hash for a path whose current stat is (size, mod).
// Every uncertain answer is a miss.
func (h *hashMemo) lookup(path string, size int64, mod time.Time, ident fileIdent) (string, bool) {
	h.mu.RLock()
	e, ok := h.m[path]
	h.mu.RUnlock()
	if !ok || e.size != size || !e.mod.Equal(mod) {
		cntMemoMiss.Add(1)
		return "", false
	}
	// A writer that restores the mtime it found — `rsync -a`, `cp -p`, an archive
	// extraction — can rewrite the same number of bytes without moving anything
	// the comparison above looks at. The inode-change time is the part it cannot
	// put back. Where the platform reports none, that rewrite is unprovable, so
	// the entry is never handed out at all; see fileIdent.
	if !ident.known || e.ident != ident {
		cntMemoMiss.Add(1)
		return "", false
	}
	// The entry was taken inside its own mtime tick, so a same-length rewrite
	// could have landed in that tick unseen. No later stat can settle that.
	if e.mod.After(e.taken.Add(-mtimeGranularity)) {
		cntMemoMiss.Add(1)
		return "", false
	}
	cntMemoHit.Add(1)
	return e.hash, true
}

// store records the hash of the bytes read from a handle whose stat was
// (size, mod), starting at taken. Callers pass the stat of the open file
// description they hashed, never a later stat of the path.
//
// The nil receiver is load-bearing: hashedState stores unconditionally, and the
// passes that must bypass the memo hand it a nil one.
func (h *hashMemo) store(path, hash string, size int64, mod time.Time, ident fileIdent, taken time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if _, exists := h.m[path]; !exists && len(h.m) >= maxMemoEntries {
		// Flush wholesale rather than evict: there is no recency to rank entries
		// by that is worth tracking for a cache whose miss is one file read.
		h.m = make(map[string]memoEntry)
	}
	h.m[path] = memoEntry{hash: hash, size: size, mod: mod, ident: ident, taken: taken}
	h.mu.Unlock()
}
