package parsermanager

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestPrefetch_DownloadsAndStoresAllParsers(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	bodyA := []byte("\x00asm-module-a")
	bodyB := []byte("\x00asm-module-b")
	srvA, hitsA := serveWASM(t, bodyA)
	srvB, hitsB := serveWASM(t, bodyB)

	parsers := config.MapOfParsers{
		"a": {URL: srvA.URL, Hash: sha256Hex(bodyA)},
		"b": {URL: srvB.URL, Hash: sha256Hex(bodyB)},
	}
	m := New(parsers)
	t.Cleanup(func() { _ = m.Close(context.Background()) })

	// Empty names → every declared parser is fetched into its store path.
	if err := m.Prefetch(context.Background(), nil); err != nil {
		t.Fatalf("Prefetch() error = %v", err)
	}
	for name, p := range parsers {
		path := filepath.Join(ModuleStorePath(name, p), WASMFileName)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("parser %q not stored at %s: %v", name, path, err)
		}
	}
	if n := atomic.LoadInt64(hitsA); n != 1 {
		t.Errorf("server A hits = %d, want 1", n)
	}
	if n := atomic.LoadInt64(hitsB); n != 1 {
		t.Errorf("server B hits = %d, want 1", n)
	}

	// Re-prefetch is a no-op (module already on disk → no extra download).
	if err := m.Prefetch(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("re-Prefetch() error = %v", err)
	}
	if n := atomic.LoadInt64(hitsA); n != 1 {
		t.Errorf("re-prefetch downloaded again: server A hits = %d, want 1", n)
	}
}

func TestPrefetch_UndeclaredParserErrors(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	m := New(config.MapOfParsers{})
	t.Cleanup(func() { _ = m.Close(context.Background()) })

	if err := m.Prefetch(context.Background(), []string{"ghost"}); err == nil {
		t.Error("Prefetch of an undeclared parser must error")
	}
	// No declarations + empty names → nothing to do, no error.
	if err := m.Prefetch(context.Background(), nil); err != nil {
		t.Errorf("Prefetch(nil) over empty config = %v, want nil", err)
	}
}

func TestModuleStorePath_MatchesInternalLayout(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	p := config.Parser{URL: "https://example.invalid/m.wasm", Hash: sha256Hex([]byte("x"))}
	if got, want := ModuleStorePath("core", p), moduleDir("core", p); got != want {
		t.Errorf("ModuleStorePath = %q, want %q (must equal the internal moduleDir)", got, want)
	}
}
