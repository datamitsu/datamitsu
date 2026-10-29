package cache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"

	"github.com/shamaton/msgpack/v2"
	"go.uber.org/zap"
)

// newWarmCache builds a real cache for root with one recorded lint pass per
// file, saved to disk, and returns the cache for its invalidation key.
func newWarmCache(t *testing.T, cacheDir, root string, files map[string]string, tool string) *Cache {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := NewCache(cacheDir, root, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	for rel := range files {
		abs := filepath.Join(root, rel)
		h, err := hashFile(abs)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AfterLint(abs, tool, Seen{Hash: h}, true, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	return c
}

func statePath(cacheDir, root string) string {
	return filepath.Join(cacheDir, "projects", env.HashProjectPath(root), cacheFileName)
}

func mustReadState(t *testing.T, path string) File {
	t.Helper()
	f, err := readToolstate(path)
	if err != nil {
		t.Fatalf("readToolstate(%s): %v", path, err)
	}
	return f
}

func writeState(t *testing.T, path string, f *File) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := msgpack.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		t.Fatal(err)
	}
}

// TestCopyProjectWarmsEmptyDestination: the whole point of the command. A
// cache warmed at one root serves a hit at another root after CopyProject.
func TestCopyProjectWarmsEmptyDestination(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	files := map[string]string{"a.txt": "alpha\n", "pkg/b.txt": "beta\n"}

	src := newWarmCache(t, cacheDir, srcRoot, files, "tool-a")

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Toolstate != ToolstateCopied {
		t.Fatalf("toolstate status = %q, want copied", report.Toolstate)
	}
	if report.ImportedEntries != len(files) {
		t.Fatalf("imported entries = %d, want %d", report.ImportedEntries, len(files))
	}

	dstState := mustReadState(t, statePath(cacheDir, dstRoot))
	if dstState.InvalidationKey != src.InvalidationKey() {
		t.Error("the imported toolstate lost the source's invalidation key")
	}
	if dstState.ProjectPath != dstRoot {
		t.Errorf("ProjectPath = %q, want the destination root", dstState.ProjectPath)
	}

	for rel, content := range files {
		abs := filepath.Join(dstRoot, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dst, err := NewCache(cacheDir, dstRoot, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	for rel := range files {
		h, err := hashFile(filepath.Join(dstRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		if dst.Check(filepath.Join(dstRoot, rel), "tool-a", OperationLint, Seen{Hash: h}, true) {
			t.Errorf("%s: the destination must not re-run after the copy", rel)
		}
	}
}

// TestCopyProjectMergePolicy pins the import rules entry by entry.
func TestCopyProjectMergePolicy(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	writeState(t, statePath(cacheDir, srcRoot), &File{
		InvalidationKey: "k",
		ProjectPath:     srcRoot,
		Entries: map[string]FileEntry{
			"same.txt":  {ContentHash: "hash-a", Lint: []string{"t1"}, Fix: []string{"tf"}},
			"other.txt": {ContentHash: "hash-c", Lint: []string{"t9"}},
			"gone.txt":  {ContentHash: "hash-d", Lint: []string{"t1"}},
		},
	})
	writeState(t, statePath(cacheDir, dstRoot), &File{
		InvalidationKey: "k",
		ProjectPath:     dstRoot,
		Version:         "v-dst",
		Entries: map[string]FileEntry{
			"same.txt":  {ContentHash: "hash-a", Lint: []string{"t2"}},
			"other.txt": {ContentHash: "hash-b", Lint: []string{"t2"}},
		},
	})

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Toolstate != ToolstateMerged {
		t.Fatalf("toolstate status = %q, want merged", report.Toolstate)
	}
	if report.ImportedEntries != 2 {
		t.Fatalf("imported entries = %d, want 2 (the union on same.txt and the new gone.txt)", report.ImportedEntries)
	}

	merged := mustReadState(t, statePath(cacheDir, dstRoot))
	if e := merged.Entries["same.txt"]; len(e.Lint) != 2 || e.Fix[0] != "tf" {
		t.Errorf("same.txt = %+v, want the union of both sides' passes", e)
	}
	if e := merged.Entries["other.txt"]; e.ContentHash != "hash-b" || e.Lint[0] != "t2" {
		t.Errorf("other.txt = %+v, want the destination entry kept whole on a hash conflict", e)
	}
	if e := merged.Entries["gone.txt"]; e.ContentHash != "hash-d" {
		t.Errorf("gone.txt = %+v, want the source entry imported", e)
	}
	if merged.Version != "v-dst" || merged.ProjectPath != dstRoot {
		t.Errorf("merged file kept Version=%q ProjectPath=%q, want the destination's", merged.Version, merged.ProjectPath)
	}
}

// TestCopyProjectResurrectedEntryCannotHit: importing an entry the destination
// no longer has is safe — a hit still requires the on-disk content to match.
func TestCopyProjectResurrectedEntryCannotHit(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	src := newWarmCache(t, cacheDir, srcRoot, map[string]string{"a.txt": "one\n"}, "tool-a")

	// The destination started as a copy of the source's tree, ran, and pruned
	// a.txt (the file was deleted); its toolstate no longer holds the entry.
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeState(t, statePath(cacheDir, dstRoot), &File{
		InvalidationKey: src.InvalidationKey(),
		ProjectPath:     dstRoot,
		Entries:         map[string]FileEntry{},
	})

	if _, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{}); err != nil {
		t.Fatal(err)
	}

	// The entry is back; the file exists again but with different bytes.
	if err := os.WriteFile(filepath.Join(dstRoot, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst, err := NewCache(cacheDir, dstRoot, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hashFile(filepath.Join(dstRoot, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !dst.Check(filepath.Join(dstRoot, "a.txt"), "tool-a", OperationLint, Seen{Hash: h}, true) {
		t.Error("a resurrected entry must not hit over different content")
	}
}

// TestCopyProjectVerdictPolicy: collisions keep the newer validation, imported
// verdicts keep their age — an expired one stays expired.
func TestCopyProjectVerdictPolicy(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	now := time.Now()

	// The destination first saves a real toolstate, so the file on disk carries
	// the invalidation key a NewCache in this process computes; hand-written
	// keys would make every later Load reset the file.
	dstWarm, err := NewCache(cacheDir, dstRoot, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := dstWarm.Save(); err != nil {
		t.Fatal(err)
	}
	dstState := mustReadState(t, statePath(cacheDir, dstRoot))

	writeState(t, statePath(cacheDir, srcRoot), &File{
		InvalidationKey: dstState.InvalidationKey,
		Entries:         map[string]FileEntry{},
		Verdicts: map[string]VerdictEntry{
			"collide": {Tool: "t", Op: "lint", InputHash: "in", ValidatedAt: now.Add(-2 * time.Hour)},
			"expired": {Tool: "t", Op: "lint", InputHash: "in", ValidatedAt: now.Add(-48 * time.Hour)},
			"fresh":   {Tool: "t", Op: "lint", InputHash: "in", ValidatedAt: now.Add(-1 * time.Minute)},
		},
	})
	dstState.Verdicts = map[string]VerdictEntry{
		"collide": {Tool: "t", Op: "lint", InputHash: "in", ValidatedAt: now.Add(-1 * time.Hour)},
	}
	writeState(t, statePath(cacheDir, dstRoot), &dstState)

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ImportedVerdicts != 2 {
		t.Fatalf("imported verdicts = %d, want 2 (expired and fresh; the collision keeps dst's newer one)", report.ImportedVerdicts)
	}

	got := mustReadState(t, statePath(cacheDir, dstRoot)).Verdicts
	if !got["collide"].ValidatedAt.Equal(now.Add(-1 * time.Hour)) {
		t.Error("a verdict collision must keep the more recent validation")
	}

	dst, err := NewCache(cacheDir, dstRoot, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if !dst.ShouldRunVerdict("expired", "in", time.Hour) {
		t.Error("an imported verdict older than the TTL must still run")
	}
	if dst.ShouldRunVerdict("fresh", "in", time.Hour) {
		t.Error("an imported verdict inside the TTL must hit")
	}
}

// TestCopyProjectKeepsIncompatibleDestination: different invalidation keys
// mean different configurations; the destination keeps its own file, bytes
// untouched.
func TestCopyProjectKeepsIncompatibleDestination(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	newWarmCache(t, cacheDir, srcRoot, map[string]string{"a.txt": "one\n"}, "tool-a")
	writeState(t, statePath(cacheDir, dstRoot), &File{
		InvalidationKey: "a-different-configuration",
		ProjectPath:     dstRoot,
		Entries:         map[string]FileEntry{"kept.txt": {ContentHash: "h", Lint: []string{"t"}}},
	})
	before, err := os.ReadFile(statePath(cacheDir, dstRoot))
	if err != nil {
		t.Fatal(err)
	}

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Toolstate != ToolstateKeptIncompatible {
		t.Fatalf("toolstate status = %q, want kept-incompatible", report.Toolstate)
	}
	after, err := os.ReadFile(statePath(cacheDir, dstRoot))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("an incompatible destination must keep its bytes untouched")
	}
}

func TestCopyProjectSkipsUnreadableSource(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	srcDir := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot))
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, cacheFileName), []byte("not msgpack"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{})
	if err != nil {
		t.Fatalf("an unreadable source is a report, not an error: %v", err)
	}
	if report.Toolstate != ToolstateSkippedInvalid {
		t.Fatalf("toolstate status = %q, want skipped-invalid", report.Toolstate)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(report.Warnings))
	}
	if _, err := os.Stat(statePath(cacheDir, dstRoot)); !os.IsNotExist(err) {
		t.Error("nothing may be written for the destination")
	}
}

func TestCopyProjectReplacesUnreadableDestination(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	newWarmCache(t, cacheDir, srcRoot, map[string]string{"a.txt": "one\n"}, "tool-a")
	dstDir := filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, cacheFileName), []byte("not msgpack"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Toolstate != ToolstateCopied {
		t.Fatalf("toolstate status = %q, want copied", report.Toolstate)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %d, want the replacement warning", len(report.Warnings))
	}
	if _, ok := mustReadState(t, statePath(cacheDir, dstRoot)).Entries["a.txt"]; !ok {
		t.Error("the source entries must replace the unreadable destination file")
	}
}

func TestCopyProjectErrors(t *testing.T) {
	cacheDir := t.TempDir()
	root := t.TempDir()

	if _, err := CopyProject(cacheDir, root, root, CopyOptions{}); !errors.Is(err, ErrSameProject) {
		t.Fatalf("error = %v, want ErrSameProject", err)
	}

	empty := t.TempDir()
	if _, err := CopyProject(cacheDir, empty, root, CopyOptions{}); !errors.Is(err, ErrNoSourceCache) {
		t.Fatalf("error = %v, want ErrNoSourceCache", err)
	}

	// A namespace holding only tool caches is a source when ToolCaches asks
	// for them; the toolstate is then simply absent.
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	srcCache := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot), "cache", "t")
	if err := os.MkdirAll(srcCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "state.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{}); !errors.Is(err, ErrNoSourceCache) {
		t.Fatalf("error = %v, want ErrNoSourceCache without tool caches", err)
	}

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Toolstate != ToolstateAbsent {
		t.Fatalf("toolstate status = %q, want absent", report.Toolstate)
	}
	if report.ToolCacheFiles != 1 {
		t.Fatalf("tool cache files = %d, want 1", report.ToolCacheFiles)
	}
}

// TestCopyProjectToolCaches: no-replace copies, mode preservation, symlink
// skipping, convergence of a re-run, and that farm files are never touched.
func TestCopyProjectToolCaches(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	srcDir := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot))
	srcCache := filepath.Join(srcDir, "cache")
	if err := os.MkdirAll(filepath.Join(srcCache, "tool1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "tool1", "f.bin"), []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcCache, "tool2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "tool2", "g.txt"), []byte("g"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcCache, "tool3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "tool3", "x.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../escaped", filepath.Join(srcCache, "tool2", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// The source-mode farm lives in the same namespace and must not move.
	if err := os.MkdirAll(filepath.Join(srcDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "lock"), []byte("l"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A file the destination already has is never replaced.
	dstTool1 := filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot), "cache", "tool1")
	if err := os.MkdirAll(dstTool1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstTool1, "f.bin"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A destination directory that is a symlink to somewhere else must not
	// receive anything: the copy stays inside its own namespace.
	outside := t.TempDir()
	outsideMarker := filepath.Join(outside, "marker")
	if err := os.WriteFile(outsideMarker, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot), "cache", "tool3")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.ToolCacheFiles != 1 || report.SkippedExisting != 1 || report.SkippedSpecial != 2 {
		t.Fatalf("report = %+v, want 1 copied, 1 existing, 2 special (the source link and the symlinked destination directory)", report)
	}
	if got, _ := os.ReadFile(outsideMarker); string(got) != "untouched" {
		t.Error("a symlinked destination directory must not receive anything")
	}
	if _, err := os.Lstat(filepath.Join(outside, "x.bin")); !os.IsNotExist(err) {
		t.Error("the copy must not write through a destination directory symlink")
	}

	dstCache := filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot), "cache")
	if got, _ := os.ReadFile(filepath.Join(dstTool1, "f.bin")); string(got) != "mine" {
		t.Error("an existing destination file must keep its bytes")
	}
	info, err := os.Stat(filepath.Join(dstCache, "tool2", "g.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("g.txt mode = %v, want 0644", info.Mode().Perm())
	}
	if _, err := os.Lstat(filepath.Join(dstCache, "tool2", "link")); !os.IsNotExist(err) {
		t.Error("a symlink must be skipped, not copied")
	}
	dstDir := filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot))
	if _, err := os.Stat(filepath.Join(dstDir, "bin")); !os.IsNotExist(err) {
		t.Error("the source-mode farm must not be copied")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "manifest.json")); !os.IsNotExist(err) {
		t.Error("the farm manifest must not be copied")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "lock")); !os.IsNotExist(err) {
		t.Error("the farm lock must not be copied")
	}

	// A re-run converges: everything now exists, nothing is replaced.
	again, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.ToolCacheFiles != 0 || again.SkippedExisting != 2 || again.SkippedSpecial != 2 {
		t.Fatalf("second report = %+v, want 0 copied, 2 existing, 2 special", again)
	}

	// An interrupted copy leaves a hole a re-run fills: delete one copied file.
	if err := os.Remove(filepath.Join(dstCache, "tool2", "g.txt")); err != nil {
		t.Fatal(err)
	}
	resume, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if resume.ToolCacheFiles != 1 {
		t.Fatalf("resume report = %+v, want the deleted file recopied", resume)
	}
	if got, _ := os.ReadFile(filepath.Join(dstCache, "tool2", "g.txt")); string(got) != "g" {
		t.Error("the re-run must restore the file's content")
	}
}

// TestCopyProjectDryRunDecidesWithoutWriting: a dry run reports the same
// decisions and leaves the destination namespace absent.
func TestCopyProjectDryRunDecidesWithoutWriting(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	newWarmCache(t, cacheDir, srcRoot, map[string]string{"a.txt": "one\n"}, "tool-a")
	srcCache := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot), "cache", "t")
	if err := os.MkdirAll(srcCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "state.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dry, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{DryRun: true, ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot))); !os.IsNotExist(err) {
		t.Error("a dry run must not create the destination namespace")
	}

	actual, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Toolstate != actual.Toolstate || dry.ImportedEntries != actual.ImportedEntries ||
		dry.ToolCacheFiles != actual.ToolCacheFiles || dry.ToolCacheBytes != actual.ToolCacheBytes {
		t.Errorf("dry run decided %+v, the real copy %+v", dry, actual)
	}
}

// TestCopyProjectRefusesSymlinkedCacheRoot: a destination whose cache/ tree
// itself is a link elsewhere copies nothing rather than writing through it.
func TestCopyProjectRefusesSymlinkedCacheRoot(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	newWarmCache(t, cacheDir, srcRoot, map[string]string{"a.txt": "one\n"}, "tool-a")
	srcCache := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot), "cache", "t")
	if err := os.MkdirAll(srcCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "state.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	dstNs := filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot))
	if err := os.MkdirAll(dstNs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dstNs, "cache")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	report, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Toolstate != ToolstateCopied {
		t.Fatalf("toolstate status = %q, want copied — the toolstate file does not live under cache/", report.Toolstate)
	}
	if report.ToolCacheFiles != 0 || report.SkippedSpecial != 1 {
		t.Fatalf("report = %+v, want nothing copied and the linked cache root counted special", report)
	}
	if _, err := os.Lstat(filepath.Join(outside, "t")); !os.IsNotExist(err) {
		t.Error("the copy must not write through the linked cache root")
	}
}

// TestCopyProjectDryRunMatchesAwkwardTargets: a dangling symlink and a
// directory at target names are "already present" to a real copy's O_EXCL,
// and the dry run must decide the same way.
func TestCopyProjectDryRunMatchesAwkwardTargets(t *testing.T) {
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	srcCache := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot), "cache", "t")
	if err := os.MkdirAll(srcCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "a.bin"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcCache, "b.bin"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	dstTool := filepath.Join(cacheDir, "projects", env.HashProjectPath(dstRoot), "cache", "t")
	if err := os.MkdirAll(dstTool, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dstTool, "nowhere"), filepath.Join(dstTool, "a.bin")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dstTool, "b.bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	dry, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{DryRun: true, ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{ToolCaches: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.ToolCacheFiles != actual.ToolCacheFiles || dry.SkippedExisting != actual.SkippedExisting ||
		dry.SkippedSpecial != actual.SkippedSpecial || dry.ToolCacheBytes != actual.ToolCacheBytes {
		t.Errorf("dry run decided %+v, the real copy %+v", dry, actual)
	}
	if actual.ToolCacheFiles != 0 || actual.SkippedExisting != 2 {
		t.Fatalf("real report = %+v, want both awkward targets counted as present", actual)
	}
	if _, err := os.Lstat(filepath.Join(dstTool, "a.bin")); err != nil {
		t.Error("the dangling symlink must survive the copy untouched")
	}
}

// TestCopyProjectUnreadableSourceNamespace: a namespace that cannot be read
// is an error, not a project without a cache.
func TestCopyProjectUnreadableSourceNamespace(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission modes do not apply")
	}
	cacheDir := t.TempDir()
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	srcNs := filepath.Join(cacheDir, "projects", env.HashProjectPath(srcRoot))
	if err := os.MkdirAll(srcNs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcNs, cacheFileName), []byte("locked away"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(srcNs, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(srcNs, 0o755) })

	if _, err := CopyProject(cacheDir, srcRoot, dstRoot, CopyOptions{}); err == nil || errors.Is(err, ErrNoSourceCache) {
		t.Fatalf("error = %v, want a filesystem error rather than a missing cache", err)
	}
}
