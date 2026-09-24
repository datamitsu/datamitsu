package lsp

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// TestNewSessionWiresCache guards against the executor being built with a nil
// cache again: formatting in the editor must warm the SAME execution cache the
// CLI uses, so a later `datamitsu fix`/`check` can skip the unchanged file.
// (Verified end-to-end that a warmed file then reports `cache 100%` for its
// per-file/globbed tools; this is the cheap regression guard.)
func TestNewSessionWiresCache(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir()) // isolate from the real cache
	ss := newSession(&config.Config{}, t.TempDir())
	if ss.cache == nil {
		t.Fatal("newSession must wire a non-nil execution cache (it was built with nil)")
	}
	ss.close()
}
