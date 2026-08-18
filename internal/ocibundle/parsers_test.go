package ocibundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/parsermanager"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestValidateSubtree_AcceptsParsers(t *testing.T) {
	if err := validateSubtree(".parsers/core/abc123"); err != nil {
		t.Errorf("validateSubtree(.parsers/core/abc123) = %v, want nil", err)
	}
	// Escapes and foreign roots still rejected.
	if err := validateSubtree(".parsers/../etc"); err == nil {
		t.Error("validateSubtree must reject a traversal under .parsers")
	}
}

func parserOnlyConfig() *config.Config {
	return &config.Config{
		Tools: config.MapOfTools{
			"eslint": {Name: "eslint", OutputParser: &config.OutputParser{Module: "core", Parser: "eslint"}},
		},
		Parsers: config.MapOfParsers{
			"core": {URL: "https://example.invalid/m.wasm", Hash: strings.Repeat("a", 64)},
		},
	}
}

func TestExpectedSubtrees_ParserReferencedByTool(t *testing.T) {
	storeRoot := testStore(t)
	cfg := parserOnlyConfig()

	// No apps needed — parsers are still included (the deliberate over-pull so the
	// airgapped parser path keeps working for any demand seed).
	expected := expectedSubtrees(cfg, storeRoot, nil, nil)
	if len(expected) != 1 {
		t.Fatalf("expected = %v, want exactly the parser subtree", expected)
	}
	for subtree, owner := range expected {
		if !strings.HasPrefix(subtree, ".parsers/core/") {
			t.Errorf("subtree = %q, want a .parsers/core/ path", subtree)
		}
		if owner != "parser core" {
			t.Errorf("owner = %q, want 'parser core'", owner)
		}
	}
}

func TestExpectedSubtrees_UnreferencedParserExcluded(t *testing.T) {
	storeRoot := testStore(t)
	// A declared parser that no tool references contributes nothing.
	cfg := &config.Config{
		Parsers: config.MapOfParsers{"core": {URL: "https://example.invalid/m.wasm", Hash: strings.Repeat("a", 64)}},
	}
	if expected := expectedSubtrees(cfg, storeRoot, nil, nil); len(expected) != 0 {
		t.Errorf("expected = %v, want empty (no tool references the parser)", expected)
	}
}

// A full seed queues every annotated layer the bundle carries, and only the
// consumer's own pins have a hash to check against — so a parser module the
// config does not declare must never reach the store. It would sit there
// unverified until the day a config pins that hash, at which point the store's
// stat fast path would hand it straight to the WASM runtime.
func TestSeed_SkipsParserLayerTheConfigDoesNotDeclare(t *testing.T) {
	storeRoot := testStore(t)
	cfg := parserOnlyConfig()

	declaredModule := []byte("declared module bytes")
	cfg.Parsers["core"] = config.Parser{URL: "https://example.invalid/m.wasm", Hash: sha256Hex(declaredModule)}
	declaredSubtree, err := subtreeRel(storeRoot, parsermanager.ModuleStorePath("core", cfg.Parsers["core"]))
	if err != nil {
		t.Fatalf("subtreeRel: %v", err)
	}
	// A second module, published by a bundle producer whose config pins a
	// different version. Same shape, different content-addressed directory.
	foreign := config.Parser{URL: "https://example.invalid/other.wasm", Hash: strings.Repeat("b", 64)}
	foreignSubtree, err := subtreeRel(storeRoot, parsermanager.ModuleStorePath("core", foreign))
	if err != nil {
		t.Fatalf("subtreeRel: %v", err)
	}

	src := newFakeSource()
	layers := []ocispec.Descriptor{
		src.addLayer(parserLayer(t, declaredSubtree, declaredModule), declaredSubtree),
		src.addLayer(parserLayer(t, foreignSubtree, []byte("foreign module bytes")), foreignSubtree),
	}
	digest := src.addManifest(t, layers, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}

	if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(declaredSubtree))); err != nil {
		t.Errorf("declared parser module was not seeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(foreignSubtree))); err == nil {
		t.Error("a parser module the config does not declare was placed in the store unverified")
	}
}

// parserLayer builds the layer for a parser module subtree: the module file
// inside the content-addressed directory.
func parserLayer(t *testing.T, subtree string, module []byte) []byte {
	t.Helper()
	prefix := strings.TrimPrefix(testBuilderRoot, "/")
	return subtreeLayer(t, subtree, []tarEntry{
		dirEntry(prefix + "/" + subtree + "/"),
		fileEntry(prefix+"/"+subtree+"/"+parsermanager.WASMFileName, module),
	})
}

func TestBuildReVerifyIndex_IndexesParser(t *testing.T) {
	storeRoot := testStore(t)
	hash := strings.Repeat("a", 64)
	cfg := &config.Config{
		Parsers: config.MapOfParsers{"core": {URL: "https://example.invalid/m.wasm", Hash: hash}},
	}

	index := buildReVerifyIndex(cfg, storeRoot)
	if len(index) != 1 {
		t.Fatalf("index entries = %d, want 1 (the parser module)", len(index))
	}
	for subtree, spec := range index {
		if !strings.HasPrefix(subtree, ".parsers/core/") {
			t.Errorf("subtree = %q, want a .parsers/core/ path", subtree)
		}
		if spec.owner != "parser core" {
			t.Errorf("owner = %q, want 'parser core'", spec.owner)
		}
		if spec.sha256 != hash {
			t.Errorf("sha256 = %q, want the published hash", spec.sha256)
		}
		if spec.relPath != parsermanager.WASMFileName {
			t.Errorf("relPath = %q, want %q", spec.relPath, parsermanager.WASMFileName)
		}
	}
}
