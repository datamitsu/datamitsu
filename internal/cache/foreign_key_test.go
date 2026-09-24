package cache

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/shamaton/msgpack/v2"

	"go.uber.org/zap"
)

// An editor session holds the config it started with. After a config edit the
// CLI writes a new key; a session that replaced it would reset the CLI's cache,
// and the CLI would reset the session's — forever. In yield mode every write
// path leaves a foreign file alone; the default still takes it over, because
// nothing else ever rewrites the on-disk key.
func TestSaveWithAForeignKeyOnDisk(t *testing.T) {
	cliConfig := config.Config{IgnoreRules: []string{"cli"}}
	staleConfig := config.Config{IgnoreRules: []string{"stale"}}

	// Each flush is one write path; all of them must honour the mode.
	flushes := map[string]func(t *testing.T, c *Cache) error{
		"Save": func(_ *testing.T, c *Cache) error { return c.Save() },
		"Shutdown": func(_ *testing.T, c *Cache) error {
			c.MarkDirty()
			c.Shutdown()
			return nil
		},
		"debounced flush": func(t *testing.T, c *Cache) error {
			t.Helper()
			c.MarkDirty()
			waitForDebounce(t, c)
			return nil
		},
	}

	tests := []struct {
		name        string
		yield       bool
		wantErr     error
		wantOnDisk  string // verdict key that must be on disk afterwards
		wantMissing string // verdict key that must not be
	}{
		{"yield mode leaves the file alone", true, ErrForeignKey, "from-cli", "from-session"},
		{"default mode takes the file over", false, nil, "from-session", "from-cli"},
	}

	for _, tt := range tests {
		for flushName, flush := range flushes {
			t.Run(tt.name+"/"+flushName, func(t *testing.T) {
				dir, project := t.TempDir(), t.TempDir()

				cli, _ := NewCache(dir, project, cliConfig, nil, zap.NewNop())
				cli.AfterVerdict("from-cli", VerdictEntry{Tool: "tsc", InputHash: "x"})
				if err := cli.Save(); err != nil {
					t.Fatalf("CLI save: %v", err)
				}
				// The CLI process has exited: its debounced flush must not race
				// the session's writes below.
				cli.Shutdown()
				before, err := os.ReadFile(cli.path)
				if err != nil {
					t.Fatal(err)
				}

				session, _ := NewCache(dir, project, staleConfig, nil, zap.NewNop())
				t.Cleanup(session.Shutdown)
				session.SetYieldToForeignKey(tt.yield)
				session.AfterVerdict("from-session", VerdictEntry{Tool: "tsc", InputHash: "y"})

				err = flush(t, session)
				if flushName == "Save" && !errors.Is(err, tt.wantErr) {
					t.Fatalf("Save() = %v, want %v", err, tt.wantErr)
				}

				after, err := os.ReadFile(cli.path)
				if err != nil {
					t.Fatal(err)
				}
				if tt.yield && !bytes.Equal(before, after) {
					t.Error("yield mode rewrote a file owned by another configuration")
				}

				onDisk := decodeOnDisk(t, cli.path)
				if _, ok := onDisk.Verdicts[tt.wantOnDisk]; !ok {
					t.Errorf("verdict %q missing on disk", tt.wantOnDisk)
				}
				if _, ok := onDisk.Verdicts[tt.wantMissing]; ok {
					t.Errorf("verdict %q present on disk, want it absent", tt.wantMissing)
				}
			})
		}
	}
}

// Yield mode only yields to a different key. With the same key, or with no file
// at all, it merges and writes like the default.
func TestYieldModeStillWritesItsOwnFile(t *testing.T) {
	t.Run("no file yet", func(t *testing.T) {
		c := newTestCache(t)
		t.Cleanup(c.Shutdown)
		c.SetYieldToForeignKey(true)
		c.AfterVerdict("k", VerdictEntry{Tool: "tsc", InputHash: "x"})
		if err := c.Save(); err != nil {
			t.Fatalf("Save() = %v, want nil", err)
		}
		if _, ok := decodeOnDisk(t, c.path).Verdicts["k"]; !ok {
			t.Error("the verdict was not written")
		}
	})

	t.Run("same key merges", func(t *testing.T) {
		dir, project := t.TempDir(), t.TempDir()
		first, _ := NewCache(dir, project, config.Config{}, nil, zap.NewNop())
		first.AfterVerdict("from-first", VerdictEntry{Tool: "a", InputHash: "x"})
		if err := first.Save(); err != nil {
			t.Fatalf("first save: %v", err)
		}
		first.Shutdown()

		session, _ := NewCache(dir, project, config.Config{}, nil, zap.NewNop())
		t.Cleanup(session.Shutdown)
		session.SetYieldToForeignKey(true)
		session.AfterVerdict("from-session", VerdictEntry{Tool: "b", InputHash: "y"})
		if err := session.Save(); err != nil {
			t.Fatalf("Save() = %v, want nil", err)
		}

		onDisk := decodeOnDisk(t, first.path)
		for _, key := range []string{"from-first", "from-session"} {
			if _, ok := onDisk.Verdicts[key]; !ok {
				t.Errorf("verdict %q lost in the merge", key)
			}
		}
	})
}

func decodeOnDisk(t *testing.T, path string) *File {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	var f File
	if err := msgpack.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode cache file: %v", err)
	}
	return &f
}

// waitForDebounce returns once the debounced flush has claimed the dirty flag
// and its save, if any, has finished.
func waitForDebounce(t *testing.T, c *Cache) {
	t.Helper()
	t.Cleanup(c.Shutdown)
	deadline := time.Now().Add(2 * time.Second)
	for c.dirty.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the debounced flush did not fire within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The timer claims the flag just before it takes saveMu.
	time.Sleep(50 * time.Millisecond)
	c.saveMu.Lock()
	c.saveMu.Unlock() //nolint:staticcheck // an empty critical section waits for the in-flight save
}

// A different binary is a cause restarting cannot fix, so the error names the
// version that wrote the file; every save records its own.
func TestForeignKeyErrorNamesTheWritersVersion(t *testing.T) {
	dir, project := t.TempDir(), t.TempDir()

	cli, _ := NewCache(dir, project, config.Config{IgnoreRules: []string{"cli"}}, nil, zap.NewNop())
	if err := cli.Save(); err != nil {
		t.Fatalf("CLI save: %v", err)
	}
	cli.Shutdown()
	if got := decodeOnDisk(t, cli.path).Version; got != ldflags.Version {
		t.Fatalf("recorded version = %q, want %q", got, ldflags.Version)
	}

	onDisk := decodeOnDisk(t, cli.path)
	onDisk.Version = "0.0.1-other"
	raw, err := msgpack.Marshal(*onDisk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli.path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	session, _ := NewCache(dir, project, config.Config{IgnoreRules: []string{"session"}}, nil, zap.NewNop())
	t.Cleanup(session.Shutdown)
	session.SetYieldToForeignKey(true)

	err = session.Save()
	var fk *ForeignKeyError
	if !errors.As(err, &fk) || !errors.Is(err, ErrForeignKey) {
		t.Fatalf("Save() = %v, want a ForeignKeyError matching ErrForeignKey", err)
	}
	if fk.DiskVersion != "0.0.1-other" {
		t.Errorf("DiskVersion = %q, want %q", fk.DiskVersion, "0.0.1-other")
	}
}
