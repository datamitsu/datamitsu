package parsermanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// sha256Hex returns the lowercase hex SHA-256 of b (the form a config hash takes).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// serveWASM starts a test server returning fixed bytes and counts requests.
func serveWASM(t *testing.T, body []byte) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestLoadWASMBytes_ValidHashStoresAndLoads(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	body := []byte("\x00asm-fake-module")
	srv, hits := serveWASM(t, body)

	m := New(config.MapOfParsers{
		"echo": {URL: srv.URL, Hash: sha256Hex(body)},
	})

	got, err := m.LoadWASMBytes(context.Background(), "echo")
	if err != nil {
		t.Fatalf("LoadWASMBytes() error = %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("LoadWASMBytes() = %q, want %q", got, body)
	}

	// The module must be content-addressed on disk under {parsers}/echo/{key}.
	dir := moduleDir("echo", m.parsers["echo"])
	if _, err := os.Stat(filepath.Join(dir, wasmFileName)); err != nil {
		t.Fatalf("module not stored at %s: %v", dir, err)
	}
	if n := atomic.LoadInt64(hits); n != 1 {
		t.Fatalf("server hits = %d, want 1", n)
	}
}

func TestLoadWASMBytes_WrongHashFails(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	body := []byte("real-bytes")
	srv, _ := serveWASM(t, body)

	m := New(config.MapOfParsers{
		"echo": {URL: srv.URL, Hash: sha256Hex([]byte("different-bytes"))},
	})

	_, err := m.LoadWASMBytes(context.Background(), "echo")
	if err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("error = %v, want it to mention hash mismatch", err)
	}
	// A failed verification must leave no published module behind.
	dir := moduleDir("echo", m.parsers["echo"])
	if _, statErr := os.Stat(filepath.Join(dir, wasmFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("module should not be published on hash mismatch (stat err = %v)", statErr)
	}
}

func TestLoadWASMBytes_MissingHashIsError(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	m := New(config.MapOfParsers{
		"echo": {URL: "https://example.test/echo.wasm"},
	})

	_, err := m.LoadWASMBytes(context.Background(), "echo")
	if err == nil || !strings.Contains(err.Error(), "no hash") {
		t.Fatalf("expected mandatory-hash error, got %v", err)
	}
}

func TestLoadWASMBytes_UndeclaredParser(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	m := New(nil)
	_, err := m.LoadWASMBytes(context.Background(), "ghost")
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected not-declared error, got %v", err)
	}
}

func TestLoadWASMBytes_SecondLoadIsCached(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	body := []byte("cache-me")
	srv, hits := serveWASM(t, body)

	m := New(config.MapOfParsers{
		"echo": {URL: srv.URL, Hash: sha256Hex(body)},
	})

	// Two references to the same parser (e.g. two tools) must download once.
	for i := range 2 {
		if _, err := m.LoadWASMBytes(context.Background(), "echo"); err != nil {
			t.Fatalf("LoadWASMBytes() call %d error = %v", i, err)
		}
	}
	if n := atomic.LoadInt64(hits); n != 1 {
		t.Fatalf("server hits = %d, want 1 (download must be deduplicated)", n)
	}
}

// TestLoadWASMBytes_ConcurrentLoadsDeduplicate exercises the singleflight
// coalescing path (not the on-disk fast path): many goroutines start together
// against a deliberately slow server, so they collide inside ensureModule. The
// server must be hit exactly once. Run under -race to catch dedup regressions.
func TestLoadWASMBytes_ConcurrentLoadsDeduplicate(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	body := []byte("concurrent-bytes")
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		// Widen the window so concurrent callers coalesce in singleflight
		// rather than each finding the module already published on disk.
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	m := New(config.MapOfParsers{
		"echo": {URL: srv.URL, Hash: sha256Hex(body)},
	})

	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = m.LoadWASMBytes(context.Background(), "echo")
		}(i)
	}
	close(start) // release all goroutines at once
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("LoadWASMBytes goroutine %d error = %v", i, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hits = %d, want 1 (concurrent loads must deduplicate)", got)
	}
}

// TestLoadWASMBytes_LocalFileSource covers the dev-link loop end to end at the
// store level: a `file://` module is verified and content-addressed exactly like
// a downloaded one, and only a dev-link build may read it at all.
func TestLoadWASMBytes_LocalFileSource(t *testing.T) {
	body := []byte("\x00asm-locally-built")
	src := filepath.Join(t.TempDir(), "datamitsu_parsers.wasm")
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatalf("write local module: %v", err)
	}

	t.Run("released build refuses", func(t *testing.T) {
		t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
		m := New(config.MapOfParsers{"core": {URL: "file://" + src, Hash: sha256Hex(body)}})
		if _, err := m.LoadWASMBytes(context.Background(), "core"); err == nil {
			t.Fatal("a build without the dev-link flag must refuse a file:// module")
		}
	})

	t.Run("dev-link build loads and verifies", func(t *testing.T) {
		t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
		orig := ldflags.LocalArtifacts
		ldflags.LocalArtifacts = "1"
		t.Cleanup(func() { ldflags.LocalArtifacts = orig })

		m := New(config.MapOfParsers{"core": {URL: "file://" + src, Hash: sha256Hex(body)}})
		got, err := m.LoadWASMBytes(context.Background(), "core")
		if err != nil {
			t.Fatalf("LoadWASMBytes() error = %v", err)
		}
		if string(got) != string(body) {
			t.Fatalf("LoadWASMBytes() = %q, want %q", got, body)
		}
		// Stored content-addressed, same as a downloaded module.
		dir := moduleDir("core", m.parsers["core"])
		if _, err := os.Stat(filepath.Join(dir, wasmFileName)); err != nil {
			t.Fatalf("module not stored at %s: %v", dir, err)
		}
	})

	t.Run("hash still mandatory", func(t *testing.T) {
		t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
		orig := ldflags.LocalArtifacts
		ldflags.LocalArtifacts = "1"
		t.Cleanup(func() { ldflags.LocalArtifacts = orig })

		missing := New(config.MapOfParsers{"core": {URL: "file://" + src}})
		if _, err := missing.LoadWASMBytes(context.Background(), "core"); err == nil {
			t.Error("a file:// module with no hash must be a configuration error")
		}

		wrong := New(config.MapOfParsers{"core": {URL: "file://" + src, Hash: strings.Repeat("0", 64)}})
		if _, err := wrong.LoadWASMBytes(context.Background(), "core"); err == nil {
			t.Error("a file:// module with the wrong hash must be rejected")
		}
	})
}
