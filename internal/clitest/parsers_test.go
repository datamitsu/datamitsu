package clitest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
)

var echoWASM = filepath.Join("..", "parsermanager", "testdata", "echo.wasm")

// TestSeedParserModuleLayout pins the harness's store path to the one the
// binary computes for a cache dir, so a change to the store layout fails here
// rather than as a silent fetch attempt in an offline scenario.
func TestSeedParserModuleLayout(t *testing.T) {
	cache := t.TempDir()
	decl := SeedParserModule(t, cache, echoWASM)

	var parsers map[string]config.Parser
	if err := json.Unmarshal([]byte(decl), &parsers); err != nil {
		t.Fatalf("declaration is not a JSON object: %v\n%s", err, decl)
	}
	p, ok := parsers[SeededParserModule]
	if !ok || len(parsers) != 1 {
		t.Fatalf("declaration = %s, want the single entry %q", decl, SeededParserModule)
	}
	data, err := os.ReadFile(echoWASM)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if p.Hash != hex.EncodeToString(sum[:]) || !strings.HasPrefix(p.URL, "https://") {
		t.Errorf("declaration = %+v, want the module's SHA-256 and an https URL", p)
	}

	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	t.Setenv("DATAMITSU_PARSERS_DIR", "")
	want := filepath.Join(parsermanager.ModuleStorePath(SeededParserModule, p), parsermanager.WASMFileName)
	seeded, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("the module is not where the binary looks for it: %v", err)
	}
	if string(seeded) != string(data) {
		t.Error("the seeded module differs from its source")
	}
}

// TestSeedParserModuleOffline proves the binary loads a seeded module with
// DATAMITSU_OFFLINE set, which refuses every fetch.
func TestSeedParserModuleOffline(t *testing.T) {
	p := NewProject(t)
	cache := t.TempDir()
	cfg := p.WriteFile("parsers.config.js", ShellConfig(ShellConfigSpec{Parsers: SeedParserModule(t, cache, echoWASM)}))

	res := Run(t, RunOptions{Dir: p.Dir, CacheDir: cache},
		"--no-auto-config", "--config", cfg, "devtools", "parsers", "list", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("devtools parsers list exit = %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, `"hadolint"`) {
		t.Errorf("the seeded module's catalogue should list hadolint:\n%s", res.Stdout)
	}
}
