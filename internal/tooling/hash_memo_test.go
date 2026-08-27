package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The point of Task 3: the second tool planning a task over the same unit reads
// no bytes at all, because the content of a file does not depend on who asks.
func TestContentMemoReadsEachFileOncePerProcess(t *testing.T) {
	root, members, guards := probeFixture(t)
	memo := newHashMemo()

	first, firstBytes := verdictSnapshotWith(members, guards, root, memo)
	if firstBytes == 0 {
		t.Fatal("precondition: the first pass should have read the unit")
	}

	second, secondBytes := verdictSnapshotWith(members, guards, root, memo)
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

	before, _ := verdictSnapshotWith([]string{member}, nil, root, memo)

	writeUnder(t, member, "export const a = 1 // rewritten, and longer\n")
	backdate(t, member)

	after, bytesRead := verdictSnapshotWith([]string{member}, nil, root, memo)
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
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	memo := newHashMemo()

	if _, bytesRead := verdictSnapshotWith([]string{member}, nil, root, memo); bytesRead == 0 {
		t.Fatal("precondition: the first pass should have read the file")
	}
	if _, bytesRead := verdictSnapshotWith([]string{member}, nil, root, memo); bytesRead == 0 {
		t.Error("a file modified inside the current mtime tick was answered from the memo")
	}

	backdate(t, member)
	if _, bytesRead := verdictSnapshotWith([]string{member}, nil, root, memo); bytesRead == 0 {
		t.Fatal("precondition: backdating changes the mtime, so this pass re-reads")
	}
	if _, bytesRead := verdictSnapshotWith([]string{member}, nil, root, memo); bytesRead != 0 {
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

	snap, _ := verdictSnapshotWith([]string{member}, nil, root, memo)

	writeUnder(t, member, "export const a = 2 // and rather longer\n")
	backdate(t, member)
	fi, err := os.Stat(member)
	if err != nil {
		t.Fatal(err)
	}
	poison := strings.Repeat("0f", 16)
	memo.store(member, poison, fi.Size(), fi.ModTime())

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
			snap, _ := verdictSnapshotWith(members, nil, root, memo)
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
	memo.store("/p", "abc", 10, old)

	tests := []struct {
		name string
		size int64
		mod  time.Time
		now  time.Time
		want bool
	}{
		{"same stat", 10, old, time.Now(), true},
		{"different size", 11, old, time.Now(), false},
		{"different mtime", 10, old.Add(time.Second), time.Now(), false},
		{"inside the mtime tick", 10, old, old.Add(time.Second), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := memo.lookup("/p", tt.size, tt.mod, tt.now); ok != tt.want {
				t.Errorf("lookup ok = %v, want %v", ok, tt.want)
			}
		})
	}
	if _, ok := memo.lookup("/other", 10, old, time.Now()); ok {
		t.Error("an unknown path was answered from the memo")
	}
}
