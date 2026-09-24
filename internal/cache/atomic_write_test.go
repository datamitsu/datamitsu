package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/shamaton/msgpack/v2"

	"go.uber.org/zap"
)

// An editor session and a CLI run save the same cache file at the same time.
// Every version a reader can see must decode: two writers sharing one temp file
// would publish a mix of both. Losing a verdict to an interleaved merge is
// allowed, but writers that take turns keep each other's.
func TestConcurrentSavesNeverPublishAMix(t *testing.T) {
	dir, project := t.TempDir(), t.TempDir()

	writers := make([]*Cache, 2)
	for i := range writers {
		c, err := NewCache(dir, project, config.Config{}, nil, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Shutdown)
		writers[i] = c
	}
	// Sizes differ, so bytes from the two writers cannot line up by accident.
	for i := range 400 {
		writers[0].AfterVerdict(fmt.Sprintf("big-%03d", i), VerdictEntry{Tool: "eslint", UnitDir: strings.Repeat("d", 64), InputHash: "h"})
	}
	writers[1].AfterVerdict("small", VerdictEntry{Tool: "tsc", InputHash: "h"})

	const rounds = 150
	path := writers[0].path
	done := make(chan struct{})
	var readerErrs []error
	var readerWG sync.WaitGroup
	readerWG.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			raw, err := os.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				readerErrs = append(readerErrs, err)
				return
			}
			var f File
			if err := msgpack.Unmarshal(raw, &f); err != nil {
				readerErrs = append(readerErrs, fmt.Errorf("published file does not decode (%d bytes): %w", len(raw), err))
				return
			}
		}
	})

	var writerWG sync.WaitGroup
	errs := make(chan error, len(writers)*rounds)
	for i, c := range writers {
		writerWG.Go(func() {
			for n := range rounds {
				c.AfterVerdict(fmt.Sprintf("w%d-%d", i, n), VerdictEntry{Tool: "t", InputHash: "h"})
				if err := c.Save(); err != nil {
					errs <- err
				}
			}
		})
	}
	writerWG.Wait()
	close(done)
	readerWG.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("Save: %v", err)
	}
	for _, err := range readerErrs {
		t.Error(err)
	}

	for _, c := range writers {
		c.Shutdown() // no debounced save may interleave with the turns below
	}
	for _, c := range []*Cache{writers[1], writers[0]} {
		if err := c.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	final := decodeOnDisk(t, path)
	for _, key := range []string{"small", "big-000"} {
		if _, ok := final.Verdicts[key]; !ok {
			t.Errorf("verdict %q is missing after the writers took turns", key)
		}
	}
	assertNoTempFiles(t, filepath.Dir(path))
}

// A save killed between creating its temp file and renaming it strands the
// file, and no later save reuses its name. Loading the cache removes abandoned
// ones and leaves alone a write that may still be in flight.
func TestNewCacheRemovesAbandonedTempFiles(t *testing.T) {
	cacheDir, project := t.TempDir(), t.TempDir()
	c, err := NewCache(cacheDir, project, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	c.Shutdown()
	dir := filepath.Dir(c.path)

	files := []struct {
		name     string
		age      time.Duration
		wantKept bool
	}{
		{name: "toolstate-123.tmp", age: 2 * staleTempAge},
		{name: "toolstate.msgpack.tmp", age: 2 * staleTempAge},
		{name: "toolstate-456.tmp", wantKept: true},
		{name: "unrelated-789.tmp", age: 2 * staleTempAge, wantKept: true},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := time.Now().Add(-f.age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	again, err := NewCache(cacheDir, project, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(again.Shutdown)

	for _, f := range files {
		_, err := os.Stat(filepath.Join(dir, f.name))
		if kept := err == nil; kept != f.wantKept {
			t.Errorf("%s (age %s): kept = %v, want %v", f.name, f.age, kept, f.wantKept)
		}
	}
}

// Shutdown waits for a debounced save that is already writing: a process that
// exits as soon as Shutdown returns would otherwise strand its temp file.
func TestShutdownWaitsForARunningFlush(t *testing.T) {
	c, err := NewCache(t.TempDir(), t.TempDir(), config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	// Holding saveMu parks the debounced flush inside Save, past its dirty check.
	c.saveMu.Lock()
	c.MarkDirty()
	for deadline := time.Now().Add(5 * time.Second); c.dirty.Load(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			c.saveMu.Unlock()
			t.Fatal("the debounced flush never started")
		}
	}

	returned := make(chan struct{})
	go func() {
		c.Shutdown()
		close(returned)
	}()
	select {
	case <-returned:
		c.saveMu.Unlock()
		t.Fatal("Shutdown returned while a flush was still saving")
	case <-time.After(50 * time.Millisecond):
	}
	c.saveMu.Unlock()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return once the flush finished")
	}
	if _, err := os.Stat(c.path); err != nil {
		t.Errorf("the flush Shutdown waited for wrote nothing: %v", err)
	}
}

// A failed write must not leave its temp file behind: with a unique name per
// write, nothing would ever overwrite it again.
func TestWriteFileAtomicRemovesItsTempFileOnFailure(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{
			// Renaming a file over a non-empty directory fails on every platform.
			name: "rename fails",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, cacheFileName)
			tt.setup(t, path)

			if err := writeFileAtomic(path, []byte("data")); err == nil {
				t.Fatal("writeFileAtomic() = nil, want an error")
			}
			assertNoTempFiles(t, dir)
		})
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", cacheFileName)

	for _, content := range []string{"first", "second, longer than the first", "3"} {
		if err := writeFileAtomic(path, []byte(content)); err != nil {
			t.Fatalf("writeFileAtomic(%q) = %v", content, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("file = %q, want %q", got, content)
		}
	}
	assertNoTempFiles(t, filepath.Dir(path))
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}
