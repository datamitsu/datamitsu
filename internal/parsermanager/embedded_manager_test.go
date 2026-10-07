package parsermanager

import (
	"context"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// TestTheManagerServesTheEmbeddedModule: the fallback built into the binary is
// a module like the declared ones — described, compiled once, pooled — without
// being declared, fetched or listed.
func TestTheManagerServesTheEmbeddedModule(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	ctx := context.Background()
	m := New(config.MapOfParsers{})
	t.Cleanup(func() { _ = m.Close(ctx) })

	if err := m.Prewarm(ctx, []string{EmbeddedModule}); err != nil {
		t.Fatalf("Prewarm(embedded) error = %v", err)
	}
	if known, err := m.HasParser(ctx, EmbeddedModule, FallbackParser); err != nil || !known {
		t.Fatalf("HasParser(embedded, fallback) = %v, %v", known, err)
	}
	if contract, err := m.SeverityContract(ctx, EmbeddedModule); err != nil || !contract {
		t.Errorf("SeverityContract(embedded) = %v, %v; want the contract", contract, err)
	}
	if known, _ := m.HasParser(ctx, EmbeddedModule, "eslint"); known {
		t.Error("the embedded module describes a tool parser")
	}
	for range 3 {
		resp, err := m.Fallback(ctx, []byte("::error file=a.py,line=2::m\n"), nil, 1)
		if err != nil {
			t.Fatalf("Fallback() error = %v", err)
		}
		if !resp.Recognized || resp.Format != "github-annotations" || len(resp.Diagnostics) != 1 {
			t.Fatalf("Fallback() = %+v", resp)
		}
	}
	unknown, err := m.Fallback(ctx, []byte("All checks passed!\n"), nil, 0)
	if err != nil || unknown.Recognized {
		t.Errorf("Fallback(prose) = %+v, %v; want not recognized", unknown, err)
	}
	cut, err := m.Fallback(ctx, []byte(`{"version":"2.1.0","runs":[{"results":[`), nil, 0)
	if err != nil || cut.Recognized || !cut.Partial {
		t.Errorf("Fallback(cut-off SARIF) = %+v, %v; want not recognized and partial", cut, err)
	}
	deep, err := m.Fallback(ctx, []byte(strings.Repeat("[", 200_000)+strings.Repeat("]", 200_000)), nil, 0)
	if err != nil || deep.Recognized {
		t.Errorf("Fallback(deeply nested JSON) = recognized %v, %v; want an answer without a trap", deep.Recognized, err)
	}
	cat, err := m.ListCapabilities(ctx)
	if err != nil {
		t.Fatalf("ListCapabilities() error = %v", err)
	}
	if len(cat.Tools) != 0 {
		t.Errorf("ListCapabilities() lists the embedded module: %+v", cat.Tools)
	}
	facts, described := m.DescribedParser(EmbeddedModule, FallbackParser)
	if !described || facts.Tool.Kind != "format" {
		t.Errorf("DescribedParser(embedded, fallback) = %+v, %v", facts, described)
	}
}

func TestDescribeAndParseEmbedded(t *testing.T) {
	ctx := context.Background()
	caps, err := DescribeEmbedded(ctx)
	if err != nil || caps.ABI != 2 {
		t.Fatalf("DescribeEmbedded() = %+v, %v", caps, err)
	}
	resp, err := ParseEmbedded(ctx, "gcc", []byte("a.c:1:2: warning: w\n"), nil, 0)
	if err != nil || !resp.Recognized || len(resp.Diagnostics) != 1 {
		t.Errorf("ParseEmbedded(gcc) = %+v, %v", resp, err)
	}
}
