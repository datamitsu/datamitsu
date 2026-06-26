package ocibundle

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
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
