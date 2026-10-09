package parsermanager

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// A parser pin's spelling must never move the module's content-addressed
// directory: the canonical migration rewrites config pins, not the store.
func TestCacheKeySpellingStability(t *testing.T) {
	bare := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	canonical := "sha256:" + bare
	upper := "sha256:BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD"

	gotBare := cacheKey(config.Parser{URL: "https://example.com/m.wasm", Hash: bare})
	gotCanonical := cacheKey(config.Parser{URL: "https://example.com/m.wasm", Hash: canonical})
	gotUpper := cacheKey(config.Parser{URL: "https://example.com/m.wasm", Hash: upper})

	if gotBare == "" || gotBare != gotCanonical || gotBare != gotUpper {
		t.Fatalf("pin spelling moved the module store key: bare=%s canonical=%s upper=%s", gotBare, gotCanonical, gotUpper)
	}
}
