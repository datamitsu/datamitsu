package configcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/shamaton/msgpack/v2"
)

// isolatedCache points env.GetCachePath() at a temp tree.
func isolatedCache(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	cachePath := env.GetCachePath()
	if !strings.HasPrefix(cachePath, base) {
		t.Fatalf("cache path %q is not inside the temp base %q", cachePath, base)
	}
	return cachePath
}

// sampleConfig is a merged config with enough shape that a serializer dropping a
// field shows up in the round trip.
func sampleConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Apps: binmanager.MapOfApps{
			"task": {
				Required:    true,
				Description: "task runner",
				Binary: &binmanager.AppConfigBinary{
					Version: "3.44.0",
				},
			},
		},
		Setup: config.MapOfConfigSetup{
			"eslint": {
				ProjectTypes: []string{"node"},
				Scope:        "project",
				// A live goja value in the real thing; anything non-nil is
				// enough to prove Save drops it.
				Content: func() {},
			},
		},
		IgnoreRules:   []string{"**/dist/**"},
		SharedStorage: map[string]string{"go": "gopath"},
	}
}

// newTestStore isolates the cache and returns a store for a fresh git root.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	isolatedCache(t)
	return storeForRoot(t, filepath.Join(t.TempDir(), "repo"))
}

// storeForRoot returns a store inside the cache the caller already isolated.
func storeForRoot(t *testing.T, gitRoot string) *Store {
	t.Helper()
	ns, err := ProjectNamespace(gitRoot)
	if err != nil {
		t.Fatalf("ProjectNamespace: %v", err)
	}
	s, err := NewStore(ns)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// testKey is a 32-hex-digit key, spelled as a repeat so it is neither a
// plausible secret nor an unknown word to the spell checker.
var testKey = strings.Repeat("ab", 16)

func TestStoreRoundTrip(t *testing.T) {
	s := newTestStore(t)
	cfg := sampleConfig(t)

	warnings := []string{"app foo: no lock file"}
	remotes := []string{"https://example.com/config.js"}
	if err := s.Save(testKey, &Entry{Config: cfg, Warnings: warnings, RemoteURLs: remotes}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := s.Load(testKey)
	if !ok {
		t.Fatal("Load: miss after Save")
	}

	// Warnings and resolved remotes ride along, so a hit can reproduce the
	// output a miss produced.
	if !slices.Equal(got.Warnings, warnings) {
		t.Errorf("warnings = %v, want %v", got.Warnings, warnings)
	}
	if !slices.Equal(got.RemoteURLs, remotes) {
		t.Errorf("remote URLs = %v, want %v", got.RemoteURLs, remotes)
	}

	// Setup content cannot survive and must not be faked.
	for name, entry := range got.Config.Setup {
		if entry.Content != nil {
			t.Errorf("setup %q: Content survived the round trip, want nil", name)
		}
	}
	if cfg.Setup["eslint"].Content == nil {
		t.Error("Save mutated the caller's config: Content was cleared in place")
	}

	// The whole graph, not counts: strip content from the original and compare
	// serialized forms.
	want := withoutSetupContent(cfg)
	before, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(got.Config)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("config did not survive the store round trip:\n before %s\n after  %s", before, after)
	}
}

func TestStoreArtifactPathIsUnderTheCacheTree(t *testing.T) {
	cachePath := isolatedCache(t)
	ns, err := ProjectNamespace(filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(ns)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(testKey, &Entry{Config: sampleConfig(t)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := filepath.Join(cachePath, DirName, filepath.FromSlash(ns), testKey+artifactExt)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("artifact not at %q: %v", want, err)
	}
	if got := env.GetStorePath(); strings.HasPrefix(want, got) {
		t.Errorf("artifact %q is inside the store %q, must be in the deletable cache tree", want, got)
	}
}

func TestStoreMissOnEmptyStore(t *testing.T) {
	s := newTestStore(t)
	if _, ok := s.Load(testKey); ok {
		t.Error("Load: hit on an empty store")
	}
}

func TestStoreCorruptArtifactIsAMissAndIsRemoved(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
	}{
		{"garbage", []byte("not msgpack at all")},
		{"empty", []byte{}},
		{"truncated", nil}, // filled in below from a real artifact
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if err := s.Save(testKey, &Entry{Config: sampleConfig(t)}); err != nil {
				t.Fatalf("Save: %v", err)
			}
			p := filepath.Join(s.Dir(), testKey+artifactExt)

			data := tc.bytes
			if tc.name == "truncated" {
				full, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				data = full[:len(full)/2]
			}
			if err := os.WriteFile(p, data, 0o600); err != nil {
				t.Fatal(err)
			}

			if _, ok := s.Load(testKey); ok {
				t.Error("Load: hit on a corrupt artifact, want a miss")
			}
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("corrupt artifact still present at %q (stat err %v)", p, err)
			}
		})
	}
}

func TestStoreUnknownFormatVersionIsAMiss(t *testing.T) {
	s := newTestStore(t)
	encoded, err := msgpack.Marshal(artifact{FormatVersion: FormatVersion + 1, Config: withoutSetupContent(sampleConfig(t))})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Dir(), testKey+artifactExt)
	if err := os.WriteFile(p, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.Load(testKey); ok {
		t.Error("Load: hit on an artifact from another format version, want a miss")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("artifact with a foreign format version still present (stat err %v)", err)
	}
}

// TestStoreConcurrentWritersProduceAReadableArtifact pins the reason the write
// goes through a temp file and a rename: a reader must never observe a partial
// artifact, no matter how many processes write the same key at once.
func TestStoreConcurrentWritersProduceAReadableArtifact(t *testing.T) {
	s := newTestStore(t)
	cfg := sampleConfig(t)

	const writers = 8
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Go(func() {
			errs[i] = s.Save(testKey, &Entry{Config: cfg})
		})
	}
	// Read while the writers run: a rename is atomic, so every read either
	// misses or decodes.
	wg.Go(func() {
		for range 50 {
			if got, ok := s.Load(testKey); ok && (got == nil || got.Config == nil) {
				t.Error("Load reported a hit with a nil config")
			}
		}
	})
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: %v", i, err)
		}
	}
	if _, ok := s.Load(testKey); !ok {
		t.Error("Load: miss after concurrent writes")
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp files left behind: %v", names)
	}
}

func TestPruneRemovesUnreadEntriesAndEmptyDirs(t *testing.T) {
	cachePath := isolatedCache(t)
	s := storeForRoot(t, filepath.Join(t.TempDir(), "repo"))
	if err := s.Save(testKey, &Entry{Config: sampleConfig(t)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fresh := filepath.Join(s.Dir(), testKey+artifactExt)

	stale := filepath.Join(s.Dir(), strings.Repeat("f", 32)+artifactExt)
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-MaxAge - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	Prune(filepath.Join(cachePath, DirName), MaxAge)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale artifact survived the prune (stat err %v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh artifact was pruned: %v", err)
	}

	// A namespace whose every entry expired goes with them.
	orphanNS, err := ProjectNamespace(filepath.Join(t.TempDir(), "gone"))
	if err != nil {
		t.Fatal(err)
	}
	orphanDir := filepath.Join(cachePath, DirName, filepath.FromSlash(orphanNS))
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(orphanDir, testKey+artifactExt)
	if err := os.WriteFile(orphan, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	Prune(filepath.Join(cachePath, DirName), MaxAge)

	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("emptied namespace directory survived the prune (stat err %v)", err)
	}
}

// TestSavePrunes proves the prune runs on the write path, which is the only
// place it can run without costing the hit path anything.
func TestSavePrunes(t *testing.T) {
	isolatedCache(t)
	s := storeForRoot(t, filepath.Join(t.TempDir(), "repo"))
	if err := os.MkdirAll(s.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(s.Dir(), strings.Repeat("f", 32)+artifactExt)
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-MaxAge - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(testKey, &Entry{Config: sampleConfig(t)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("Save did not prune the stale entry (stat err %v)", err)
	}
}

// TestLoadRefreshesAnAgingEntry pins the "unread for N days" semantics: a hit
// must keep an old entry alive, or a config nobody edits expires under a user
// who runs it every day.
func TestLoadRefreshesAnAgingEntry(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(testKey, &Entry{Config: sampleConfig(t)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := filepath.Join(s.Dir(), testKey+artifactExt)
	old := time.Now().Add(-MaxAge / 2)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.Load(testKey); !ok {
		t.Fatal("Load: miss")
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > refreshInterval {
		t.Errorf("a hit did not refresh the entry: mtime is %v old", time.Since(info.ModTime()))
	}
}

func TestNamespaces(t *testing.T) {
	isolatedCache(t)

	t.Run("project namespace matches the farm identity", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		ns, err := ProjectNamespace(root)
		if err != nil {
			t.Fatal(err)
		}
		want := env.ProjectFarmsDirName + "/" + env.HashProjectPath(root)
		if ns != want {
			t.Errorf("ProjectNamespace = %q, want %q", ns, want)
		}
	})

	t.Run("two roots do not share a namespace", func(t *testing.T) {
		a, err := ProjectNamespace(filepath.Join(t.TempDir(), "a"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := ProjectNamespace(filepath.Join(t.TempDir(), "b"))
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Errorf("two git roots share the namespace %q", a)
		}
	})

	t.Run("chain namespace matches the explicit-config farm identity", func(t *testing.T) {
		chain := []string{filepath.Join(t.TempDir(), "a.js"), filepath.Join(t.TempDir(), "b.js")}
		ns, err := ChainNamespace(chain)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := env.ConfigFarmIdentity(chain)
		if err != nil {
			t.Fatal(err)
		}
		if want := env.ConfigFarmsDirName + "/" + identity; ns != want {
			t.Errorf("ChainNamespace = %q, want %q", ns, want)
		}
	})

	t.Run("a project namespace never collides with a chain namespace", func(t *testing.T) {
		dir := t.TempDir()
		project, err := ProjectNamespace(dir)
		if err != nil {
			t.Fatal(err)
		}
		chain, err := ChainNamespace([]string{filepath.Join(dir, "datamitsu.config.js")})
		if err != nil {
			t.Fatal(err)
		}
		if project == chain {
			t.Errorf("project and chain namespaces collide at %q", project)
		}
	})

	t.Run("rejects bad inputs", func(t *testing.T) {
		if _, err := ProjectNamespace(""); err == nil {
			t.Error("ProjectNamespace(\"\") = nil error")
		}
		if _, err := ProjectNamespace("relative/root"); err == nil {
			t.Error("ProjectNamespace with a relative root = nil error")
		}
		if _, err := ChainNamespace(nil); err == nil {
			t.Error("ChainNamespace(nil) = nil error")
		}
	})
}

func TestNewStoreRejectsAnUnsafeNamespace(t *testing.T) {
	isolatedCache(t)
	for _, ns := range []string{"", "projects", "projects/", "projects/../../etc", "elsewhere/abc123", "projects/not-hex"} {
		if _, err := NewStore(ns); err == nil {
			t.Errorf("NewStore(%q) = nil error, want a refusal", ns)
		}
	}
}

func TestStoreRejectsAKeyThatIsNotAHexDigest(t *testing.T) {
	s := newTestStore(t)
	for _, key := range []string{"", "../escape", "not-hex", "ABCDEF"} {
		if err := s.Save(key, &Entry{Config: sampleConfig(t)}); err == nil {
			t.Errorf("Save(%q) = nil error, want a refusal", key)
		}
		if _, ok := s.Load(key); ok {
			t.Errorf("Load(%q) = hit, want a miss", key)
		}
	}
}

func TestClearAllRemovesTheTree(t *testing.T) {
	cachePath := isolatedCache(t)
	s := storeForRoot(t, filepath.Join(t.TempDir(), "repo"))
	if err := s.Save(testKey, &Entry{Config: sampleConfig(t)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := ClearAll(cachePath); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cachePath, DirName)); !os.IsNotExist(err) {
		t.Errorf("config-eval tree survived ClearAll (stat err %v)", err)
	}
	if _, ok := s.Load(testKey); ok {
		t.Error("Load: hit after ClearAll")
	}
	if err := ClearAll(cachePath); err != nil {
		t.Errorf("ClearAll on an already-clear tree: %v", err)
	}
	if err := ClearAll(""); err == nil {
		t.Error("ClearAll(\"\") = nil error")
	}
}

func TestClearProjectRemovesOnlyItsNamespace(t *testing.T) {
	cachePath := isolatedCache(t)

	mine := filepath.Join(t.TempDir(), "mine")
	other := filepath.Join(t.TempDir(), "other")
	stores := map[string]*Store{}
	for _, root := range []string{mine, other} {
		ns, err := ProjectNamespace(root)
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewStore(ns)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Save(testKey, &Entry{Config: sampleConfig(t)}); err != nil {
			t.Fatal(err)
		}
		stores[root] = s
	}

	if err := ClearProject(cachePath, mine); err != nil {
		t.Fatalf("ClearProject: %v", err)
	}
	if _, ok := stores[mine].Load(testKey); ok {
		t.Error("cleared project still hits")
	}
	if _, ok := stores[other].Load(testKey); !ok {
		t.Error("ClearProject removed another project's artifacts")
	}
}
