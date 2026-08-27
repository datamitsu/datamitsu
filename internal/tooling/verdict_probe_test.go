package tooling

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/trace"
)

// backdate moves a path's timestamps an hour into the past, which is what an
// ordinary source file looks like: modified long before the run started, so the
// snapshot's mtime-tick guard does not have to fall back to a re-hash.
func backdate(t *testing.T, paths ...string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, p := range paths {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

func writeUnder(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// probeFixture is a unit of three members and two guards, one of which does not
// exist yet — everything present is backdated so the stat path is live.
func probeFixture(t *testing.T) (root string, members, guards []string) {
	t.Helper()
	root = t.TempDir()
	unit := filepath.Join(root, "pkg")
	members = []string{
		filepath.Join(unit, "a.ts"),
		filepath.Join(unit, "b.ts"),
		filepath.Join(unit, "c.ts"),
	}
	for i, m := range members {
		writeUnder(t, m, "export const a = "+string(rune('0'+i))+"\n")
	}
	present := filepath.Join(unit, "tsconfig.json")
	writeUnder(t, present, `{"compilerOptions":{}}`)
	absent := filepath.Join(unit, ".eslintrc.json")
	guards = []string{present, absent}

	backdate(t, append(append([]string{}, members...), present)...)
	return root, members, guards
}

// The whole of Task 2 rests on one claim: the stat probe reaches the same
// verdict as reading every file again. Anything it decides differently is a
// tool that runs when it should not, or — far worse — one that does not.
func TestVerdictProbeAgreesWithAFullRehash(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, members, guards []string)
		want   string // "same" or "changed" relative to the pre-run vector
	}{
		{"nothing moved", func(t *testing.T, _, _ []string) { t.Helper() }, "same"},
		{
			// Same bytes, new mtime: the stat is inconclusive, so the probe
			// re-hashes and must arrive back at "unchanged".
			"a member touched but not edited",
			func(t *testing.T, members, _ []string) {
				t.Helper()
				now := time.Now()
				if err := os.Chtimes(members[0], now, now); err != nil {
					t.Fatal(err)
				}
			},
			"same",
		},
		{
			"a member rewritten",
			func(t *testing.T, members, _ []string) {
				t.Helper()
				writeUnder(t, members[1], "export const a = 99\n")
			},
			"changed",
		},
		{
			"a member deleted",
			func(t *testing.T, members, _ []string) {
				t.Helper()
				if err := os.Remove(members[2]); err != nil {
					t.Fatal(err)
				}
			},
			"changed",
		},
		{
			"a guard edited",
			func(t *testing.T, _, guards []string) {
				t.Helper()
				writeUnder(t, guards[0], `{"compilerOptions":{"strict":true}}`)
			},
			"changed",
		},
		{
			// A guard that was absent when the unit was hashed and exists now is a
			// new input, not the absence the pre-run vector recorded.
			"a guard appearing",
			func(t *testing.T, _, guards []string) {
				t.Helper()
				writeUnder(t, guards[1], `{"rules":{}}`)
			},
			"changed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, members, guards := probeFixture(t)
			snap, _ := verdictSnapshotOf(members, guards, root)

			tt.mutate(t, members, guards)

			probed, _ := snap.refresh()
			full := verdictInputs(members, guards, root)
			if probed != full {
				t.Fatalf("the probe and a full re-hash disagree: %q vs %q", probed, full)
			}
			if (probed == snap.inputs) != (tt.want == "same") {
				t.Errorf("probe reported %q, want %q", map[bool]string{true: "same", false: "changed"}[probed == snap.inputs], tt.want)
			}
		})
	}
}

// The case the whole design has to survive: a write the filesystem's mtime
// resolution cannot distinguish from the state that was hashed. Length alone
// must catch it — nothing else in the stat has moved.
func TestVerdictProbeCatchesARewriteInsideOneMtimeTick(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")
	backdate(t, member)

	fi, err := os.Stat(member)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := verdictSnapshotOf([]string{member}, nil, root)

	// Rewrite it and put the old timestamp back: to a stat, only the size moved.
	writeUnder(t, member, "export const a = 1 // and more\n")
	if err := os.Chtimes(member, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	after, _ := snap.refresh()
	if after == snap.inputs {
		t.Fatal("a rewrite that preserved the mtime went unnoticed; the pass would be a lie")
	}
	if full := verdictInputs([]string{member}, nil, root); after != full {
		t.Errorf("probe = %q, full re-hash = %q", after, full)
	}
}

// A file that was modified moments before the run cannot be cleared by a stat at
// all: a write during the run could land in the same tick and look identical. The
// probe has to read it rather than trust it.
func TestVerdictProbeRehashesFilesModifiedDuringTheTick(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "export const a = 1\n")

	snap, _ := verdictSnapshotOf([]string{member}, nil, root)
	if snap.settled(snap.members[0]) {
		t.Error("a file modified inside the current mtime tick was accepted on its stat alone")
	}

	backdate(t, member)
	snap, _ = verdictSnapshotOf([]string{member}, nil, root)
	if !snap.settled(snap.members[0]) {
		t.Error("an untouched, backdated file still forced a re-hash; the probe saves nothing")
	}
}

// A path the probe cannot stat is never assumed unchanged.
func TestVerdictProbeRehashesWhatItCannotStat(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "a.ts")
	writeUnder(t, member, "x")
	backdate(t, member)

	snap, _ := verdictSnapshotOf([]string{member}, nil, root)
	if err := os.Remove(member); err != nil {
		t.Fatal(err)
	}
	if snap.settled(snap.members[0]) {
		t.Fatal("a vanished member was treated as unchanged")
	}
	if after, _ := snap.refresh(); after == snap.inputs {
		t.Error("a deleted member left the input vector unchanged")
	}
}

// The point of the change: a lint's post-run check reads nothing when nothing
// moved, where it used to read the whole unit a second time.
func TestVerdictProbeReadsNothingWhenNothingMoved(t *testing.T) {
	root, members, guards := probeFixture(t)

	trace.Reset()
	trace.SetEnabled(true)
	t.Cleanup(func() {
		trace.SetEnabled(false)
		trace.Reset()
	})

	snap, first := verdictSnapshotOf(members, guards, root)
	if first == 0 {
		t.Fatal("precondition: the pre-run pass should have read the unit")
	}
	if _, probed := snap.refresh(); probed != 0 {
		t.Errorf("the post-run probe read %d bytes, want 0", probed)
	}
	if got := counterValue(t, "cache.verdict_bytes_hashed"); got != first {
		t.Errorf("cache.verdict_bytes_hashed = %d, want %d — the second pass is meant to be free", got, first)
	}
}

// A fix rewrites files by design, which is exactly the write an mtime tick can
// hide, so it keeps the full second pass. The scenario below is invisible to a
// stat — same length, timestamp restored — and the fix must still record the
// state it produced rather than the one it started from.
func TestRecordVerdictFixStillRehashesEverything(t *testing.T) {
	e, root := newVerdictExecutor(t)
	task := unitTask(root)
	task.Operation = config.OpFix
	task.OpConfig = config.ToolOperation{App: "tsc", Args: []string{"--write"}, Scope: config.ToolScopePerProject}
	member := task.UnitMembers[0]
	writeUnder(t, member, "aaaa")
	backdate(t, member)

	fi, err := os.Stat(member)
	if err != nil {
		t.Fatal(err)
	}
	key, snap, ok := e.verdictKeys(task)
	if !ok {
		t.Fatal("precondition: the verdict cache should apply to this task")
	}

	// What a formatter does: same length, and a filesystem coarse enough to
	// report the same mtime — emulated exactly by restoring it.
	writeUnder(t, member, "bbbb")
	if err := os.Chtimes(member, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	e.recordVerdict(task, key, snap, ok)

	produced := verdictInputs(task.UnitMembers, task.UnitGuards, root)
	if e.cache.ShouldRunVerdict(key, produced, time.Hour) {
		t.Error("the fix recorded the state it started from; a stat probe would have missed the rewrite")
	}
	if !e.cache.ShouldRunVerdict(key, snap.hash(), time.Hour) {
		t.Error("the fix recorded the pre-run state, which the file no longer holds")
	}
}

// BenchmarkVerdictProbe is the other half of BenchmarkVerdictInputs: what the
// post-run check costs now that an unchanged unit is answered by stats. The two
// numbers together are the saving on every task that misses the cache.
func BenchmarkVerdictProbe(b *testing.B) {
	root := b.TempDir()
	unitDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		b.Fatal(err)
	}
	const members = 2000
	content := make([]byte, 2048)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	old := time.Now().Add(-time.Hour)
	paths := make([]string, 0, members)
	for i := range members {
		p := filepath.Join(unitDir, fmt.Sprintf("f%04d.ts", i))
		if err := os.WriteFile(p, content, 0o644); err != nil {
			b.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			b.Fatal(err)
		}
		paths = append(paths, p)
	}
	snap, _ := verdictSnapshotOf(paths, nil, root)

	b.SetBytes(int64(members * len(content)))
	b.ResetTimer()
	for range b.N {
		if hash, _ := snap.refresh(); hash != snap.inputs {
			b.Fatal("an untouched unit reported a change")
		}
	}
}
