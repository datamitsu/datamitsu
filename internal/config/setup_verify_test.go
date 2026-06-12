package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/dop251/goja"
)

// layer is a tiny helper to build a SetupLayerEntry with generated content.
func layer(name, content string) SetupLayerEntry {
	return SetupLayerEntry{LayerName: name, GeneratedContent: &content}
}

func TestVerifyChainHashes(t *testing.T) {
	// The "incoming" content for a root-layer pin is the output of the layer
	// just before the root layer (the upstream chain), hashed byte-for-byte.
	upstream := "version: 2\nfoo: bar\n"
	upstreamHash := hashutil.XXH3Hex([]byte(upstream))

	t.Run("no pin → no mismatch", func(t *testing.T) {
		lm := SetupLayerMap{
			".golangci.yaml": {
				FileName: ".golangci.yaml",
				Layers:   []SetupLayerEntry{layer("base", upstream), layer("root", "out")},
			},
		}
		if got := VerifyChainHashes(lm); len(got) != 0 {
			t.Fatalf("expected no mismatches, got %+v", got)
		}
	})

	t.Run("matching pin → no mismatch", func(t *testing.T) {
		lm := SetupLayerMap{
			".golangci.yaml": {
				FileName:    ".golangci.yaml",
				FinalConfig: ConfigSetup{ExpectChainHash: "xxh3:" + upstreamHash},
				// root layer (last) is excluded; its input is the "base" output.
				Layers: []SetupLayerEntry{layer("base", upstream), layer("root", "transformed")},
			},
		}
		if got := VerifyChainHashes(lm); len(got) != 0 {
			t.Fatalf("expected match, got mismatches %+v", got)
		}
	})

	t.Run("bare hex pin (no prefix) accepted", func(t *testing.T) {
		lm := SetupLayerMap{
			"x": {
				FileName:    "x",
				FinalConfig: ConfigSetup{ExpectChainHash: upstreamHash},
				Layers:      []SetupLayerEntry{layer("base", upstream), layer("root", "t")},
			},
		}
		if got := VerifyChainHashes(lm); len(got) != 0 {
			t.Fatalf("expected bare-hex match, got %+v", got)
		}
	})

	t.Run("drifted upstream → mismatch with details", func(t *testing.T) {
		lm := SetupLayerMap{
			".golangci.yaml": {
				FileName:    ".golangci.yaml",
				FinalConfig: ConfigSetup{ExpectChainHash: "xxh3:1234567890abcdef1234567890abcdef"},
				Layers:      []SetupLayerEntry{layer("base", upstream), layer("root", "t")},
			},
		}
		got := VerifyChainHashes(lm)
		if len(got) != 1 {
			t.Fatalf("expected 1 mismatch, got %d", len(got))
		}
		m := got[0]
		if m.Expected != "xxh3:1234567890abcdef1234567890abcdef" {
			t.Errorf("expected pin unchanged, got %q", m.Expected)
		}
		if m.Actual != "xxh3:"+upstreamHash {
			t.Errorf("actual = %q, want xxh3:%s", m.Actual, upstreamHash)
		}
		if m.Incoming != upstream {
			t.Errorf("incoming = %q, want the upstream (pre-root) content", m.Incoming)
		}
	})

	t.Run("intermediate-layer pin is ignored (only root consulted)", func(t *testing.T) {
		// FinalConfig carries the root layer's config; a pin that would only have
		// matched an intermediate layer must not be honoured. Here the root pin is
		// empty, so despite multiple layers there is no check.
		lm := SetupLayerMap{
			"x": {
				FileName: "x",
				Layers:   []SetupLayerEntry{layer("a", "one"), layer("b", "two"), layer("root", "three")},
			},
		}
		if got := VerifyChainHashes(lm); len(got) != 0 {
			t.Fatalf("expected no check without a root pin, got %+v", got)
		}
	})

	t.Run("single root layer falls back to original disk content", func(t *testing.T) {
		disk := "user file on disk\n"
		lm := SetupLayerMap{
			"x": {
				FileName:        "x",
				OriginalContent: &disk,
				FinalConfig:     ConfigSetup{ExpectChainHash: "xxh3:" + hashutil.XXH3Hex([]byte(disk))},
				Layers:          []SetupLayerEntry{layer("root", "generated")},
			},
		}
		if got := VerifyChainHashes(lm); len(got) != 0 {
			t.Fatalf("expected disk-content match, got %+v", got)
		}
	})
}

// TestVerifyChainHashes_Pipeline drives the real evaluation pipeline — a shared
// "base" layer followed by a "root" layer that both transforms the content AND
// pins it — to confirm the gate hashes the content ENTERING the root layer (the
// base output), not the root layer's own output.
func TestVerifyChainHashes_Pipeline(t *testing.T) {
	vm := goja.New()

	baseFn, err := vm.RunString(`(function(ctx){ return "version: 2\n"; })`)
	if err != nil {
		t.Fatalf("base fn: %v", err)
	}
	// The root layer appends an override onto whatever the upstream chain produced.
	rootFn, err := vm.RunString(`(function(ctx){ return (ctx.existingContent || "") + "extra: true\n"; })`)
	if err != nil {
		t.Fatalf("root fn: %v", err)
	}

	const baseOut = "version: 2\n"
	goodPin := "xxh3:" + hashutil.XXH3Hex([]byte(baseOut))

	build := func(rootPin string) SetupLayerMap {
		lm := make(SetupLayerMap)
		baseCfg := &Config{Setup: MapOfConfigSetup{".golangci.yaml": {Content: baseFn}}}
		MergeSetupLayers(lm, "base", EvaluateInitContent(baseCfg, vm, "/root", "/root", lm), baseCfg.Setup)

		rootCfg := &Config{Setup: MapOfConfigSetup{".golangci.yaml": {Content: rootFn, ExpectChainHash: rootPin}}}
		MergeSetupLayers(lm, "root", EvaluateInitContent(rootCfg, vm, "/root", "/root", lm), rootCfg.Setup)
		return lm
	}

	t.Run("incoming is the base output, not the root output", func(t *testing.T) {
		lm := build(goodPin)
		if got := incomingToRootLayer(lm[".golangci.yaml"]); got != baseOut {
			t.Fatalf("incoming = %q, want base output %q", got, baseOut)
		}
		// Sanity: the root layer really did transform on top of it.
		if last := GetLastGeneratedContent(lm[".golangci.yaml"]); last == nil || !strings.Contains(*last, "extra: true") {
			t.Fatalf("root layer output unexpected: %v", last)
		}
		if mm := VerifyChainHashes(lm); len(mm) != 0 {
			t.Fatalf("expected match, got %+v", mm)
		}
	})

	t.Run("stale pin after upstream drift reports base output", func(t *testing.T) {
		lm := build("xxh3:" + strings.Repeat("0", 32))
		mm := VerifyChainHashes(lm)
		if len(mm) != 1 {
			t.Fatalf("expected 1 mismatch, got %d", len(mm))
		}
		if mm[0].Incoming != baseOut {
			t.Errorf("incoming = %q, want base output %q", mm[0].Incoming, baseOut)
		}
		if mm[0].Actual != goodPin {
			t.Errorf("actual = %q, want %q", mm[0].Actual, goodPin)
		}
	})
}
