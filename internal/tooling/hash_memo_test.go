package tooling

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The point of Task 3: the second tool planning a task over the same unit reads
// no bytes at all, because the content of a file does not depend on who asks.
func TestContentMemoReadsEachFileOncePerProcess(t *testing.T) {
	requireIdent(t)
	root, members, guards := probeFixture(t)
	memo := newHashMemo()

	first, firstBytes := verdictSnapshotMode(members, guards, root, memo, memoShared)
	if firstBytes == 0 {
		t.Fatal("precondition: the first pass should have read the unit")
	}

	second, secondBytes := verdictSnapshotMode(members, guards, root, memo, memoShared)
	if secondBytes != 0 {
		t.Errorf("the second tool read %d bytes, want 0 — the memo saved nothing", secondBytes)
	}
	if second.inputs != first.inputs {
		t.Errorf("memoized input vector = %q, first pass = %q", second.inputs, first.inputs)
	}
	if full := verdictInputs(members, guards, root); second.inputs != full {
		t.Errorf("memoized input vector = %q, full re-hash = %q", second.inputs, full)
	}
}

// A memo whose entries could outlive the bytes they describe would be a cache
// that lies. A rewrite between two tasks must be read again.
func TestContentMemoRehashesAFileRewrittenBetweenTasks(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	backdate(t, member)
	memo := newHashMemo()

	before, _ := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared)

	writeUnder(t, member, "export const a = 1 // rewritten, and longer\n")
	backdate(t, member)

	after, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared)
	if bytesRead == 0 {
		t.Error("a rewritten file was answered from the memo without being read")
	}
	if after.inputs == before.inputs {
		t.Fatal("a rewritten file produced the same input vector; every later run would skip on a lie")
	}
	if full := verdictInputs([]string{member}, nil, root); after.inputs != full {
		t.Errorf("memoized input vector = %q, full re-hash = %q", after.inputs, full)
	}
}

// A file written moments ago cannot be cleared by a stat: a later write can land
// in the same tick at the same length. The memo must decline it, exactly as the
// post-run probe does.
func TestContentMemoDeclinesEntriesInsideTheMtimeTick(t *testing.T) {
	requireIdent(t)
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	memo := newHashMemo()

	if _, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared); bytesRead == 0 {
		t.Fatal("precondition: the first pass should have read the file")
	}
	if _, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared); bytesRead == 0 {
		t.Error("a file modified inside the current mtime tick was answered from the memo")
	}

	backdate(t, member)
	if _, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared); bytesRead == 0 {
		t.Fatal("precondition: backdating changes the mtime, so this pass re-reads")
	}
	if _, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared); bytesRead != 0 {
		t.Error("a settled, backdated file was still read; the memo saves nothing")
	}
}

// The single most important property of the task: recordVerdict's post-run probe
// asks whether the inputs moved while the tool ran, and answering it from the
// memo the pre-run pass filled would compare a value against itself.
//
// The memo is poisoned with a wrong hash stamped with the file's *current* stat,
// so a probe that consulted it would return that hash and see no change.
func TestPostRunProbeDoesNotConsultTheMemo(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	backdate(t, member)
	memo := newHashMemo()

	snap, _ := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared)

	writeUnder(t, member, "export const a = 2 // and rather longer\n")
	backdate(t, member)
	fi, err := os.Stat(member)
	if err != nil {
		t.Fatal(err)
	}
	poison := strings.Repeat("0f", 16)
	memo.store(member, poison, fi.Size(), fi.ModTime(), identOf(fi), time.Now())

	after, bytesRead := snap.refresh()
	if bytesRead == 0 {
		t.Error("the probe read nothing after a rewrite; it answered from a cache")
	}
	if strings.Contains(after, poison) {
		t.Error("the probe folded the poisoned memo entry into the input vector")
	}
	if full := verdictInputs([]string{member}, nil, root); after != full {
		t.Errorf("probe = %q, full re-hash = %q — the probe did not read the bytes", after, full)
	}
	if after == snap.inputs {
		t.Fatal("the probe missed a rewrite; the recorded pass would be a lie")
	}
}

// The production path, not an injected memo: verdictSnapshotOf is what the
// executor calls, and it must read through the process-scoped contentMemo.
// Wiring it to a nil memo — deleting the optimization outright — leaves every
// other memo test in this file green, so this is the one that pins it.
func TestVerdictSnapshotOfReadsThroughTheProcessMemo(t *testing.T) {
	requireIdent(t)
	root := t.TempDir()
	member := filepath.Join(root, "pkg", "shared.ts")
	writeUnder(t, member, strings.Repeat("export const x = 1\n", 32))
	backdate(t, member)
	members := []string{member}
	traceOn(t) // counters only move while tracing is on

	// The first task over this unit pays for the read; the counter proves it was
	// this call that did, since the path is unique to this test's TempDir.
	first, firstBytes := verdictSnapshotOf(members, nil, root)
	if firstBytes == 0 {
		t.Fatal("precondition: the first pass should have read the unit")
	}

	hitsBefore := counterValue(t, "cache.verdict_hash_memo_hits")
	second, secondBytes := verdictSnapshotOf(members, nil, root)
	if secondBytes != 0 {
		t.Errorf("the second task read %d bytes, want 0 — verdictSnapshotOf bypassed the process memo", secondBytes)
	}
	if got := counterValue(t, "cache.verdict_hash_memo_hits") - hitsBefore; got == 0 {
		t.Error("the second task recorded no memo hit")
	}
	if second.inputs != first.inputs {
		t.Errorf("memoized input vector = %q, first pass = %q", second.inputs, first.inputs)
	}
	if full := verdictInputs(members, nil, root); second.inputs != full {
		t.Errorf("memoized input vector = %q, full re-hash = %q", second.inputs, full)
	}
}

// The OpFix second pass reads every path because a fixer's rewrite is the one
// write a stat cannot see. Reading is only half of it: the pre-fix hashes it
// just disproved must not survive in the memo, because `check` runs lint after
// fix in the same process and would be handed them.
func TestFixRehashReplacesTheEntriesItDisproved(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	backdate(t, member)

	// The pre-run pass, through the process memo the executor really uses.
	pre, _ := verdictSnapshotOf([]string{member}, nil, root)

	// A fixer rewrites the file at the same length and leaves the stat where it
	// found it — the exact write the OpFix full re-hash exists to catch.
	fi, err := os.Stat(member)
	if err != nil {
		t.Fatal(err)
	}
	writeUnder(t, member, "export const a = 2\n")
	if err := os.Chtimes(member, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	after := verdictInputs([]string{member}, nil, root)
	if after == pre.inputs {
		t.Fatal("precondition: the fix re-hash did not notice the rewrite")
	}

	// The next task of the same run must see the fixed bytes, not the memo entry
	// the pre-run pass left behind.
	next, bytesRead := verdictSnapshotOf([]string{member}, nil, root)
	if next.inputs == pre.inputs {
		t.Errorf("the next task was handed the pre-fix hash (%d bytes read); "+
			"an unrelated stale verdict would hit", bytesRead)
	}
	if next.inputs != after {
		t.Errorf("next task input vector = %q, post-fix re-hash = %q", next.inputs, after)
	}
}

// The memo is shared by the executor's worker pool, so every access is
// concurrent by construction. Run under -race.
func TestContentMemoUnderConcurrentUse(t *testing.T) {
	root := t.TempDir()
	const files = 8
	members := make([]string, 0, files)
	for i := range files {
		p := filepath.Join(root, "pkg", "f"+string(rune('a'+i))+".ts")
		writeUnder(t, p, strings.Repeat("x", 64+i))
		members = append(members, p)
	}
	backdate(t, members...)

	memo := newHashMemo()
	want := verdictInputs(members, nil, root)

	var wg sync.WaitGroup
	got := make([]string, 16)
	for i := range got {
		wg.Go(func() {
			snap, _ := verdictSnapshotMode(members, nil, root, memo, memoShared)
			got[i] = snap.inputs
		})
	}
	wg.Wait()

	for i, h := range got {
		if h != want {
			t.Fatalf("goroutine %d computed %q, want %q", i, h, want)
		}
	}
}

// The validity check in isolation: an entry is handed out only for a stat that
// still matches the one it was taken under.
func TestHashMemoLookupValidity(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	memo := newHashMemo()
	memo.store("/p", "abc", 10, old, testIdent, old.Add(time.Hour))

	tests := []struct {
		name string
		size int64
		mod  time.Time
		want bool
	}{
		{"same stat", 10, old, true},
		{"different size", 11, old, false},
		{"different mtime", 10, old.Add(time.Second), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := memo.lookup("/p", tt.size, tt.mod, testIdent); ok != tt.want {
				t.Errorf("lookup ok = %v, want %v", ok, tt.want)
			}
		})
	}
	if _, ok := memo.lookup("/other", 10, old, testIdent); ok {
		t.Error("an unknown path was answered from the memo")
	}
}

// The memo hands entries out across the whole process — a session, under
// `datamitsu lsp` — so a writer that restores what it found has ample room to
// race it. A same-length rewrite with the original mtime put back moves neither
// half of the (size, mtime) pair, and the entry is far older than the tick
// guard, so the change time is the only thing left to catch it.
func TestContentMemoRehashesASameLengthRewriteWithRestoredMtime(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	backdate(t, member)
	memo := newHashMemo()

	first, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared)
	if bytesRead == 0 {
		t.Fatal("precondition: the first pass should have read the file")
	}
	fi, err := os.Stat(member)
	if err != nil {
		t.Fatal(err)
	}

	writeUnder(t, member, "export const a = 2\n") // same length, different bytes
	if err := os.Chtimes(member, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	second, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared)
	if bytesRead == 0 {
		t.Error("the second pass read nothing; the memo answered for bytes that no longer exist")
	}
	if second.inputs == first.inputs {
		t.Fatal("a same-length rewrite with the mtime restored was served from the memo")
	}
	if full := verdictInputs([]string{member}, nil, root); second.inputs != full {
		t.Errorf("memoized input vector = %q, full re-hash = %q", second.inputs, full)
	}
}

// The tick guard is a property of the read, not of the lookup. An entry taken
// inside its own mtime tick can never be settled by waiting: the same-length
// rewrite it cannot rule out already happened, and no later stat can see it.
func TestHashMemoNeverSettlesAnEntryTakenInsideItsTick(t *testing.T) {
	// mod and taken land in the same tick — the hash was computed on a file
	// written moments earlier — and the lookup happens an hour later.
	mod := time.Now().Add(-time.Hour)
	memo := newHashMemo()
	memo.store("/p", "abc", 10, mod, testIdent, mod.Add(time.Millisecond))

	if _, ok := memo.lookup("/p", 10, mod, testIdent); ok {
		t.Error("an entry taken inside its own mtime tick was handed out after waiting; " +
			"a same-length rewrite inside that tick would be skipped on a lie")
	}
}

// The same property through the real read path: a file hashed while its mtime is
// still fresh is re-read on every later pass, however long the wait.
func TestContentMemoDeclinesATickBoundEntryOnLaterPasses(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	memo := newHashMemo()

	if _, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared); bytesRead == 0 {
		t.Fatal("precondition: the first pass should have read the file")
	}

	// Move the whole episode an hour into the past — file mtime and entry alike —
	// so the only thing that changed is how long ago it happened. That is exactly
	// what a lookup-anchored guard would mistake for settled.
	memo.mu.Lock()
	e := memo.m[member]
	e.mod, e.taken = e.mod.Add(-time.Hour), e.taken.Add(-time.Hour)
	memo.m[member] = e
	memo.mu.Unlock()
	if err := os.Chtimes(member, e.mod, e.mod); err != nil {
		t.Fatal(err)
	}

	if _, bytesRead := verdictSnapshotMode([]string{member}, nil, root, memo, memoShared); bytesRead == 0 {
		t.Error("a file hashed inside its own mtime tick was answered from the memo an hour later")
	}
}

// The memo is process-scoped, and a `datamitsu lsp` server's process lasts a
// session. Without a cap it would retain one entry per path it ever hashed, so
// growth past the cap must drop entries rather than accumulate them.
func TestContentMemoIsBounded(t *testing.T) {
	memo := newHashMemo()
	// mod well before taken, so the mtime-tick guard does not turn the lookups
	// below into misses for a reason this test is not about.
	mod := time.Now().Add(-time.Hour)
	taken := time.Now()

	for i := range maxMemoEntries + 1 {
		memo.store("/p/"+strconv.Itoa(i), "h", 1, mod, testIdent, taken)
	}

	memo.mu.RLock()
	size := len(memo.m)
	memo.mu.RUnlock()
	if size > maxMemoEntries {
		t.Errorf("memo holds %d entries after %d stores, want at most %d", size, maxMemoEntries+1, maxMemoEntries)
	}
	// The store that tripped the cap must still be there: a flush that dropped
	// the value it was called with would make the memo lose the read it just paid for.
	if _, ok := memo.lookup("/p/"+strconv.Itoa(maxMemoEntries), 1, mod, testIdent); !ok {
		t.Error("the entry that tripped the cap was not stored")
	}
}

// Re-storing a path already in the memo must not count toward growth: a run that
// re-hashes the same working set forever would otherwise flush on a schedule.
func TestContentMemoOverwriteDoesNotTripTheCap(t *testing.T) {
	memo := newHashMemo()
	mod := time.Now().Add(-time.Hour)
	taken := time.Now()

	for i := range maxMemoEntries {
		memo.store("/p/"+strconv.Itoa(i), "h", 1, mod, testIdent, taken)
	}
	memo.store("/p/0", "h2", 1, mod, testIdent, taken)

	memo.mu.RLock()
	size := len(memo.m)
	memo.mu.RUnlock()
	if size != maxMemoEntries {
		t.Errorf("memo holds %d entries after an overwrite at the cap, want %d", size, maxMemoEntries)
	}
	if got, ok := memo.lookup("/p/0", 1, mod, testIdent); !ok || got != "h2" {
		t.Errorf("lookup after overwrite = %q, %v; want \"h2\", true", got, ok)
	}
}
