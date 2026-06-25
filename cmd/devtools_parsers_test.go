package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

func TestLoadParserCatalog_FromWasmFixture(t *testing.T) {
	cat, err := loadParserCatalog(newWasmCmd(t))
	if err != nil {
		t.Fatalf("loadParserCatalog() error = %v", err)
	}
	if len(cat.Tools) != 1 || cat.Tools[0].Name != "echo" {
		t.Fatalf("catalog tools = %+v, want exactly [echo]", cat.Tools)
	}
	if cat.Tools[0].Module != "datamitsu-parsers" || cat.Tools[0].Version == "" {
		t.Errorf("module/version not attributed from describe: %+v", cat.Tools[0])
	}
}

func TestRenderTool_Deterministic(t *testing.T) {
	color.NoColor = true // pin: no ANSI, like NO_COLOR / non-TTY in CI

	cat, err := loadParserCatalog(newWasmCmd(t))
	if err != nil {
		t.Fatalf("loadParserCatalog() error = %v", err)
	}
	tool := cat.Tools[0]

	line := renderToolLine(tool)
	// echo advertises no operation recipes, so the modes column is "-".
	if !strings.HasPrefix(line, "echo") || !strings.Contains(line, "[-]") {
		t.Errorf("list line = %q, want it to start with echo and show [-] modes", line)
	}

	detail := renderToolDetail(tool)
	for _, want := range []string{"module:   datamitsu-parsers", "parser:   (local)", "modes:    (none)"} {
		if !strings.Contains(detail, want) {
			t.Errorf("inspect detail missing %q\n%s", want, detail)
		}
	}
}
