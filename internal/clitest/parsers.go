package clitest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
)

// SeededParserModule is the parsers entry SeedParserModule declares.
const SeededParserModule = "core"

// SeedParserModule places the WASM module at modulePath into the parser store of
// a run whose DATAMITSU_CACHE_DIR is cacheDir, and returns the JS literal of a
// parsers field declaring it as SeededParserModule. The declaration carries the
// module's real SHA-256 and a URL that never resolves: the store already holds
// bytes matching the hash, so an offline run loads them without a fetch.
func SeedParserModule(tb testing.TB, cacheDir, modulePath string) string {
	tb.Helper()
	data, err := os.ReadFile(modulePath)
	if err != nil {
		tb.Fatalf("clitest: read parser module %s: %v", modulePath, err)
	}
	pin := digest.SHA256Of(data)
	decl := config.Parser{
		URL:  "https://parsers.example.invalid/module.wasm",
		Hash: pin.String(),
	}

	dst := filepath.Join(parserStoreDir(tb, cacheDir, decl), parsermanager.WASMFileName)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		tb.Fatalf("clitest: create parser store dir: %v", err)
	}
	// G703: dst is built from the calling test's own cache dir, not untrusted input.
	if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec
		tb.Fatalf("clitest: seed parser module: %v", err)
	}

	return jsLiteral(map[string]config.Parser{SeededParserModule: decl})
}

// parserStoreDir is the content-addressed directory the subprocess resolves for
// decl. BaseEnv strips DATAMITSU_PARSERS_DIR, so the subprocess keeps its parser
// store in the default place under DATAMITSU_CACHE_DIR; the part below that is
// taken from parsermanager, relative to this process's own parser store, so the
// harness never re-derives the key.
func parserStoreDir(tb testing.TB, cacheDir string, decl config.Parser) string {
	tb.Helper()
	rel, err := filepath.Rel(env.GetParsersPath(), parsermanager.ModuleStorePath(SeededParserModule, decl))
	if err != nil {
		tb.Fatalf("clitest: locate the parser store layout: %v", err)
	}
	return filepath.Join(cacheDir, "store", ".parsers", rel)
}
