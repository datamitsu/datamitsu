package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/parsermanager"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// echoFixture is the committed real Rust→WASM module, reachable from the cmd test
// dir. Using --wasm against it exercises describe end to end with no network.
func echoFixture() string {
	return filepath.Join("..", "internal", "parsermanager", "testdata", "echo.wasm")
}

// newWasmCmd builds a throwaway command carrying the parsers flags, with --wasm
// pointed at the echo fixture and a real context (cobra's Context() is nil until
// Execute, which we bypass).
func newWasmCmd(t *testing.T) *cobra.Command {
	t.Helper()
	c := &cobra.Command{}
	c.SetContext(context.Background())
	addParsersFlags(c)
	if err := c.Flags().Set("wasm", echoFixture()); err != nil {
		t.Fatalf("set --wasm: %v", err)
	}
	return c
}

// findTool returns the catalog entry for name (catalog tools are sorted, so the
// index is not stable as parsers are added).
func findTool(t *testing.T, cat *parsermanager.ParserCatalog, name string) parsermanager.CatalogTool {
	t.Helper()
	for _, tool := range cat.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not in catalog %+v", name, cat.Tools)
	return parsermanager.CatalogTool{}
}

func TestLoadParserCatalog_FromWasmFixture(t *testing.T) {
	cat, err := loadParserCatalog(newWasmCmd(t))
	if err != nil {
		t.Fatalf("loadParserCatalog() error = %v", err)
	}
	if len(cat.Tools) < 4 {
		t.Fatalf("catalog has %d tools, want >= 4: %+v", len(cat.Tools), cat.Tools)
	}
	echo := findTool(t, cat, "echo")
	if echo.Module != "datamitsu-parsers" || echo.Version == "" {
		t.Errorf("module/version not attributed from describe: %+v", echo)
	}
}

func TestRenderTool_Deterministic(t *testing.T) {
	color.NoColor = true // pin: no ANSI, like NO_COLOR / non-TTY in CI

	cat, err := loadParserCatalog(newWasmCmd(t))
	if err != nil {
		t.Fatalf("loadParserCatalog() error = %v", err)
	}

	// echo advertises no operation recipes → modes column "-", "(none)" detail.
	echo := findTool(t, cat, "echo")
	line := renderToolLine(echo)
	if !strings.HasPrefix(line, "echo") || !strings.Contains(line, "[-]") {
		t.Errorf("list line = %q, want it to start with echo and show [-] modes", line)
	}
	detail := renderToolDetail(echo)
	for _, want := range []string{"module:   datamitsu-parsers", "parser:   (local)", "modes:    (none)"} {
		if !strings.Contains(detail, want) {
			t.Errorf("inspect detail missing %q\n%s", want, detail)
		}
	}

	// yamllint advertises a lint recipe → [lint] and its args in the detail.
	yam := findTool(t, cat, "yamllint")
	if l := renderToolLine(yam); !strings.Contains(l, "[lint]") {
		t.Errorf("yamllint list line = %q, want [lint] modes", l)
	}
	if d := renderToolDetail(yam); !strings.Contains(d, "lint: --format parsable -") {
		t.Errorf("yamllint detail missing invocation recipe:\n%s", d)
	}
}
