package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/shamaton/msgpack/v2"
)

// observeFile is what the executor hands the cache: the file's bytes as they
// are now, hashed outside any lock. A file that cannot be read yields no hash.
func observeFile(path string) Seen {
	at := time.Now()
	f, err := os.Open(path)
	if err != nil {
		return Seen{At: at}
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return Seen{At: at}
	}
	d, err := digest.XXH3Reader(f)
	if err != nil {
		return Seen{At: at}
	}
	return Seen{Hash: d.Hex(), Size: fi.Size(), ModTime: fi.ModTime(), Identity: IdentityOf(fi), At: at}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPassIsRecordedAgainstTheBytesTheToolSaw is the markPassed reproduction.
// Appending a tool to an entry without comparing hashes let B's pass on Y join
// A's entry for X, so reverting the file to X skipped B on content it never
// saw.
func TestPassIsRecordedAgainstTheBytesTheToolSaw(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	writeFile(t, file, "X\n")
	x := observeFile(file)
	if !c.Check(file, "A", OperationLint, x, true) {
		t.Fatal("A on X: a fresh cache must run")
	}
	if err := c.AfterLint(file, "A", x, true, true); err != nil {
		t.Fatalf("AfterLint(A) error = %v", err)
	}

	writeFile(t, file, "Y\n")
	y := observeFile(file)
	if !c.Check(file, "B", OperationLint, y, true) {
		t.Fatal("B on Y: never passed, must run")
	}
	if err := c.AfterLint(file, "B", y, true, true); err != nil {
		t.Fatalf("AfterLint(B) error = %v", err)
	}

	writeFile(t, file, "X\n")
	x = observeFile(file)
	if !c.Check(file, "B", OperationLint, x, true) {
		t.Error("B was skipped on X, content it never saw")
	}
	if c.Check(file, "B", OperationLint, y, true) {
		t.Error("B passed on Y and must hit on Y")
	}
	if !c.Check(file, "A", OperationLint, x, true) {
		t.Error("A's pass on X was earned by bytes the entry no longer describes; it must run")
	}
}

func TestAfterLintRecordsNothingForBytesItCannotVouchFor(t *testing.T) {
	cases := []struct {
		name      string
		seen      func(file string) Seen
		unchanged bool
		enabled   bool
	}{
		{"the file changed during the run", observeFile, false, true},
		{"the bytes could not be hashed", func(string) Seen { return Seen{At: time.Now()} }, true, true},
		{"caching is off for the tool", observeFile, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cacheDir, projectPath, file := newProject(t)
			c, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
			if err != nil {
				t.Fatalf("NewCache() error = %v", err)
			}
			if err := c.AfterLint(file, "tool", tc.seen(file), tc.unchanged, tc.enabled); err != nil {
				t.Fatalf("AfterLint() error = %v", err)
			}
			if !c.Check(file, "tool", OperationLint, observeFile(file), true) {
				t.Error("a pass was recorded")
			}
			if len(c.data.Entries) != 0 {
				t.Errorf("entries = %v, want none", c.data.Entries)
			}
		})
	}
}

// TestAfterLintExtendsAnEntryForTheSameBytes: two tools that passed on the
// same bytes share an entry, and neither pass is lost to the other.
func TestAfterLintExtendsAnEntryForTheSameBytes(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	seen := observeFile(file)
	for _, tool := range []string{"A", "B"} {
		if err := c.AfterLint(file, tool, seen, true, true); err != nil {
			t.Fatalf("AfterLint(%s) error = %v", tool, err)
		}
	}
	rel, _ := filepath.Rel(projectPath, file)
	if got := c.data.Entries[rel].Lint; !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("Lint = %v, want [A B]", got)
	}
}

// TestCheckNeedsAHash: a file the executor could not read is never a hit,
// whatever the cache holds for its path.
func TestCheckNeedsAHash(t *testing.T) {
	cacheDir, projectPath, file := newProject(t)
	c, err := NewCache(cacheDir, projectPath, config.Config{}, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	if err := c.AfterLint(file, "tool", observeFile(file), true, true); err != nil {
		t.Fatalf("AfterLint() error = %v", err)
	}
	if !c.Check(file, "tool", OperationLint, Seen{}, true) {
		t.Error("a Seen without a hash hit the cache")
	}
}

// TestEntriesRecordedUnderTheOldRuleMiss: a cache file written under an
// earlier rule carries a key the current writer never produces, so its passes
// are dropped: those recorded before the cache-semantics component existed,
// without comparing hashes, those recorded under c2v1, from the exit code
// alone, those recorded under c1v1, from a tool that could have printed a
// format its parser read as clean, and those recorded under d3v1, where a fix
// pass followed success alone.
func TestEntriesRecordedUnderTheOldRuleMiss(t *testing.T) {
	for _, c := range []struct {
		name      string
		semantics []byte
	}{{"before the semantics component", nil}, {"c2v1", []byte("c2v1")}, {"c1v1", []byte("c1v1")}, {"d3v1", []byte("d3v1")}} {
		t.Run(c.name, func(t *testing.T) {
			entriesRecordedUnderAnOldRuleMiss(t, c.semantics)
		})
	}
}

func entriesRecordedUnderAnOldRuleMiss(t *testing.T, semantics []byte) {
	t.Helper()
	cacheDir, projectPath, file := newProject(t)
	cfg := config.Config{}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	parts := [][]byte{[]byte(ldflags.Version)}
	if semantics != nil {
		parts = append(parts, semantics)
	}
	oldKey := digest.XXH3Multi(append(parts, configJSON)...).Hex()
	rel, _ := filepath.Rel(projectPath, file)
	old := File{
		InvalidationKey: oldKey,
		ProjectPath:     projectPath,
		LastPruned:      time.Now(),
		Entries:         map[string]FileEntry{rel: {ContentHash: observeFile(file).Hash, Lint: []string{"tool"}}},
	}
	data, err := msgpack.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewCache(cacheDir, projectPath, cfg, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	if probe.InvalidationKey() == oldKey {
		t.Fatal("the invalidation key does not name the cache semantics")
	}
	if err := os.WriteFile(probe.path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := NewCache(cacheDir, projectPath, cfg, nil, logger.Logger)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	if !c.Check(file, "tool", OperationLint, observeFile(file), true) {
		t.Error("a pass recorded under the old rule was replayed")
	}
}
