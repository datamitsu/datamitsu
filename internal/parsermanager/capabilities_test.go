package parsermanager

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestDescribeLocal_Fixture(t *testing.T) {
	caps, err := DescribeLocal(context.Background(), echoWASM(t))
	if err != nil {
		t.Fatalf("DescribeLocal() error = %v", err)
	}
	if caps.Module != "datamitsu-parsers" || caps.Version == "" {
		t.Fatalf("unexpected module/version: %+v", caps)
	}
	got := make(map[string]bool, len(caps.Tools))
	for _, tc := range caps.Tools {
		got[tc.Name] = true
	}
	for _, want := range []string{"echo", "yamllint", "dotenv_linter", "cue_fmt"} {
		if !got[want] {
			t.Errorf("describe missing tool %q; got %+v", want, caps.Tools)
		}
	}
}

func TestCatalogFromCapabilities_FlattensSortsAndAttributes(t *testing.T) {
	caps := Capabilities{
		Module:  "m",
		Version: "9",
		Tools: []ToolCapability{
			{Name: "zeta"},
			{Name: "alpha", Operations: map[string]OperationRecipe{"lint": {Args: []string{"-x"}}}},
		},
	}
	cat := CatalogFromCapabilities("p", caps)
	if len(cat.Tools) != 2 || cat.Tools[0].Name != "alpha" || cat.Tools[1].Name != "zeta" {
		t.Fatalf("tools not sorted by name: %+v", cat.Tools)
	}
	if cat.Tools[0].Parser != "p" || cat.Tools[0].Version != "9" || cat.Tools[0].Module != "m" {
		t.Errorf("attribution wrong: %+v", cat.Tools[0])
	}
	if modes := cat.Tools[0].Modes(); len(modes) != 1 || modes[0] != "lint" {
		t.Errorf("modes = %v, want [lint]", modes)
	}
}

// TestListCapabilities_DeduplicatesSharedModule proves two config entries with
// the same module hash resolve to one module: it is described exactly once (one
// server hit), the tool is listed once, attributed to the alphabetically-first
// entry, with no spurious conflict.
func TestListCapabilities_DeduplicatesSharedModule(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	ctx := context.Background()

	wasm := echoWASM(t)
	srv, hits := serveWASM(t, wasm)
	hash := sha256Hex(wasm)

	m := New(config.MapOfParsers{
		"echo":  {URL: srv.URL, Hash: hash},
		"echo2": {URL: srv.URL, Hash: hash},
	})

	cat, err := m.ListCapabilities(ctx)
	if err != nil {
		t.Fatalf("ListCapabilities() error = %v", err)
	}
	// The module dispatches several tools; every one is listed exactly once and
	// attributed to the alphabetically-first config entry ("echo", not "echo2"),
	// with no conflicts (it is the same module content).
	if len(cat.Tools) < 4 {
		t.Fatalf("got %d tools, want >= 4 (echo + real parsers): %+v", len(cat.Tools), cat.Tools)
	}
	seen := make(map[string]int)
	for _, tool := range cat.Tools {
		seen[tool.Name]++
		if tool.Parser != "echo" {
			t.Errorf("tool %q attributed to %q, want first entry %q", tool.Name, tool.Parser, "echo")
		}
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("tool %q listed %d times, want 1 (deduplicated)", name, n)
		}
	}
	if len(cat.Conflicts) != 0 {
		t.Errorf("identical module must not conflict, got %v", cat.Conflicts)
	}
	if n := atomic.LoadInt64(hits); n != 1 {
		t.Fatalf("server hits = %d, want 1 (shared module described once)", n)
	}
}

func TestListCapabilities_EmptyIsEmpty(t *testing.T) {
	cat, err := New(nil).ListCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ListCapabilities(nil) error = %v", err)
	}
	if len(cat.Tools) != 0 || len(cat.Conflicts) != 0 {
		t.Errorf("empty manager should yield empty catalog, got %+v", cat)
	}
}
