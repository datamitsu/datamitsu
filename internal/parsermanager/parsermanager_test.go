package parsermanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	"github.com/datamitsu/datamitsu/internal/httpx"
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

// A module can reach the store without ever passing through a download: OCI
// bundle seeding, a restored CI cache, an image layer, a stray `cp`. The bytes
// on disk are therefore not trustworthy just for being there, and the config's
// mandatory SHA-256 has to be applied to them before they are handed to the
// WASM runtime.
func TestLoadWASMBytes_PrePlantedWrongBytesAreRejected(t *testing.T) {
	parsersDir := t.TempDir()
	t.Setenv("DATAMITSU_PARSERS_DIR", parsersDir)

	body := []byte("the declared module")
	declared := config.Parser{URL: "https://example.invalid/module.wasm", Hash: sha256Hex(body)}

	// Plant something else at exactly the path the declaration resolves to.
	dir := ModuleStorePath("echo", declared)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(dir, WASMFileName)
	if err := os.WriteFile(planted, []byte("a module nobody verified"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := New(config.MapOfParsers{"echo": declared})
	// The URL is unreachable, so a refetch is the only other outcome: whatever
	// happens, the planted bytes must not be what comes back.
	got, err := m.LoadWASMBytes(context.Background(), "echo")
	if err == nil {
		t.Fatalf("LoadWASMBytes returned %q for a module that fails its declared hash", got)
	}
	if _, statErr := os.Stat(planted); statErr == nil {
		t.Error("the mismatched module was left in the store to be picked up again")
	}
}

// The store path is derived from the module's hash alone, so the same module
// lands in the same place however it was obtained. Without that, a consumer
// pointing at their own mirror would silently stop matching the layer a bundle
// producer published for the very same bytes.
func TestModuleStorePath_SameHashDifferentSources(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	hash := sha256Hex([]byte("one module, several ways to get it"))
	release := config.Parser{URL: "https://github.com/datamitsu/datamitsu/releases/download/v1/m.wasm", Hash: hash}
	mirror := config.Parser{URL: "https://mirror.corp.example/datamitsu/m.wasm", Hash: hash}
	local := config.Parser{URL: "file:///home/dev/datamitsu/parsers/m.wasm", Hash: hash}

	want := ModuleStorePath("core", release)
	for _, p := range []config.Parser{mirror, local} {
		if got := ModuleStorePath("core", p); got != want {
			t.Errorf("ModuleStorePath for %q = %q, want %q", p.URL, got, want)
		}
	}

	// A different module still gets its own directory.
	other := config.Parser{URL: release.URL, Hash: sha256Hex([]byte("a different module"))}
	if got := ModuleStorePath("core", other); got == want {
		t.Error("two different modules share a store directory")
	}
}

// fakeOCIFetch installs a stand-in for the registry pull and returns a counter
// of how many times it was called. It writes body into destDir the same way a
// real pull does — a temp file the caller owns — so the store's publish and
// verification steps run for real.
func fakeOCIFetch(t *testing.T, body []byte) *int {
	t.Helper()
	calls := 0
	orig := fetchOCIModule
	fetchOCIModule = func(_ context.Context, _, _, _, destDir, _ string) (string, error) {
		calls++
		f, err := os.CreateTemp(destDir, "oci-blob-*")
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Write(body); err != nil {
			return "", err
		}
		return f.Name(), nil
	}
	t.Cleanup(func() { fetchOCIModule = orig })
	return &calls
}

func ociParser(hash string) config.Parser {
	return config.Parser{
		Hash: hash,
		OCI: &config.ParserOCI{
			Ref:    "ghcr.io/datamitsu/datamitsu-parsers",
			Digest: "sha256:" + strings.Repeat("ab", 32),
		},
	}
}

func TestLoadWASMBytes_OCISourceStoresAndLoads(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	body := []byte("\x00asm-from-a-registry")
	calls := fakeOCIFetch(t, body)

	m := New(config.MapOfParsers{"core": ociParser(sha256Hex(body))})

	got, err := m.LoadWASMBytes(context.Background(), "core")
	if err != nil {
		t.Fatalf("LoadWASMBytes() error = %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("LoadWASMBytes() = %q, want %q", got, body)
	}
	dir := moduleDir("core", m.parsers["core"])
	if _, err := os.Stat(filepath.Join(dir, wasmFileName)); err != nil {
		t.Fatalf("module not stored at %s: %v", dir, err)
	}

	// Second load must come off disk, exactly as the url path does.
	if _, err := m.LoadWASMBytes(context.Background(), "core"); err != nil {
		t.Fatalf("second LoadWASMBytes() error = %v", err)
	}
	if *calls != 1 {
		t.Errorf("registry fetches = %d, want 1", *calls)
	}
}

// TestLoadWASMBytes_OCISourceHashStillGates covers the layered defense: even if
// the registry chain were satisfied (here the fetch simply succeeds), bytes
// that do not match the config's mandatory SHA-256 are rejected and nothing is
// published into the store.
func TestLoadWASMBytes_OCISourceHashStillGates(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	fakeOCIFetch(t, []byte("what the registry actually served"))
	declared := ociParser(sha256Hex([]byte("what the config declares")))

	m := New(config.MapOfParsers{"core": declared})
	if _, err := m.LoadWASMBytes(context.Background(), "core"); err == nil {
		t.Fatal("LoadWASMBytes() accepted a module that fails its declared hash")
	}
	dir := moduleDir("core", declared)
	if _, err := os.Stat(filepath.Join(dir, wasmFileName)); !os.IsNotExist(err) {
		t.Errorf("a module that failed verification was published (stat err = %v)", err)
	}
}

func TestEnsureModule_SourceIsExactlyOne(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	hash := sha256Hex([]byte("x"))

	cases := map[string]struct {
		parser  config.Parser
		wantMsg string
	}{
		"neither": {config.Parser{Hash: hash}, "no source"},
		"both": {config.Parser{
			URL:  "https://example.com/core.wasm",
			Hash: hash,
			OCI:  ociParser(hash).OCI,
		}, "both url and oci"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := New(config.MapOfParsers{"core": tc.parser})
			_, err := m.LoadWASMBytes(context.Background(), "core")
			if err == nil {
				t.Fatal("expected a source error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not mention %q", err, tc.wantMsg)
			}
		})
	}
}

// TestOCISource_NoOCIFlagDoesNotDisableIt pins the semantics of --no-oci: it
// turns off the bundle store-seeding ACCELERATOR, which degrades gracefully to
// fetching. A declared parser source is not an accelerator — it is the only
// route to the bytes — so disabling it would leave no way to get the module at
// all. DATAMITSU_OFFLINE stays the single hard network gate, and the case below
// pins that it does refuse.
//
// The offline half of this pair deliberately runs the real fetch path: it must
// be the production GuardOffline that refuses, not a stand-in.
func TestOCISource_NoOCIFlagDoesNotDisableIt(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	t.Setenv("DATAMITSU_NO_OCI", "1")

	body := []byte("\x00asm-still-fetched")
	calls := fakeOCIFetch(t, body)

	m := New(config.MapOfParsers{"core": ociParser(sha256Hex(body))})
	if _, err := m.LoadWASMBytes(context.Background(), "core"); err != nil {
		t.Fatalf("LoadWASMBytes() error = %v", err)
	}
	if *calls != 1 {
		t.Errorf("registry fetches = %d under DATAMITSU_NO_OCI=1, want 1", *calls)
	}
}

func TestOCISource_OfflineIsRefused(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	m := New(config.MapOfParsers{"core": ociParser(sha256Hex([]byte("unreachable")))})
	_, err := m.LoadWASMBytes(context.Background(), "core")
	if err == nil {
		t.Fatal("LoadWASMBytes() attempted a registry pull in offline mode")
	}
	if !errors.Is(err, httpx.ErrOffline) {
		t.Errorf("error = %v, want it to wrap httpx.ErrOffline", err)
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
