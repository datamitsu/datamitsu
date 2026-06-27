package lsp

import (
	"io"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// TestNewServerWiresCache guards against the executor being built with a nil
// cache again: formatting in the editor must warm the SAME execution cache the
// CLI uses, so a later `datamitsu fix`/`check` can skip the unchanged file.
// (Verified end-to-end that a warmed file then reports `cache 100%` for its
// per-file/globbed tools; this is the cheap regression guard.)
func TestNewServerWiresCache(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir()) // isolate from the real cache
	s := NewServer(strings.NewReader(""), io.Discard, &config.Config{}, t.TempDir())
	if s.cache == nil {
		t.Fatal("NewServer must wire a non-nil execution cache (it was built with nil)")
	}
	s.cache.Shutdown()
}
