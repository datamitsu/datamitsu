package parsermanager

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestDescribeLocal_EchoFixture(t *testing.T) {
	caps, err := DescribeLocal(context.Background(), echoWASM(t))
	if err != nil {
		t.Fatalf("DescribeLocal() error = %v", err)
	}
	if caps.Module != "datamitsu-parsers" || len(caps.Tools) != 1 || caps.Tools[0].Name != "echo" {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if caps.Version == "" {
		t.Error("describe must report a non-empty version")
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
// the same url+hash resolve to one module: it is described exactly once (one
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
	if len(cat.Tools) != 1 || cat.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want exactly [echo]", cat.Tools)
	}
	if cat.Tools[0].Parser != "echo" {
		t.Errorf("provider = %q, want first entry %q", cat.Tools[0].Parser, "echo")
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
