//go:build e2e_oci

package e2e_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// parsersConfig is the subset of `config show` describing the declared parser
// modules, so a test can tell a registry-sourced pin from a url one.
type parsersConfig struct {
	Parsers map[string]struct {
		Hash string `json:"hash"`
		URL  string `json:"url"`
		OCI  *struct {
			Ref    string `json:"ref"`
			Digest string `json:"digest"`
		} `json:"oci"`
	} `json:"parsers"`
}

// declaredParsers reads the effective parser declarations out of `config show`.
func declaredParsers(t *testing.T, p *clitest.Project, cacheDir string) parsersConfig {
	t.Helper()
	res := runOnline(t, p.Dir, cacheDir, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("config show: exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	var cfg parsersConfig
	if err := json.Unmarshal([]byte(res.Stdout), &cfg); err != nil {
		t.Fatalf("config show did not emit valid JSON: %v\nstdout:\n%s", err, res.Stdout)
	}
	return cfg
}

// requireOCIParser skips unless the vendored config actually pins a parser to a
// registry. The fixture is the wrapper's PUBLISHED config, so this tier can
// only exercise a pin that already shipped; skipping with the reason is honest
// about that, where a hardcoded ref would test a pin nobody uses and a failure
// would say nothing about the released artifact.
func requireOCIParser(t *testing.T, cfg parsersConfig) string {
	t.Helper()
	for name, p := range cfg.Parsers {
		if p.OCI != nil {
			return name
		}
	}
	t.Skip("the vendored config declares no registry-sourced parser yet; re-vendor it once a wrapper release ships an oci parser pin")
	return ""
}

// TestOCIParserModulePull proves a registry-sourced parser module is actually
// fetchable, verified and loadable end to end.
//
// It has to FORCE the fetch: the executor logs a warning and returns raw output
// on any parser failure, so a broken pin, a private package or a stale digest
// would leave `lint` green and merely worse at reporting. `devtools parsers
// list` is the command with no such escape hatch — it downloads, SHA-256
// verifies, compiles under wazero and calls the module's own describe export,
// then exits non-zero if any of that fails.
func TestOCIParserModulePull(t *testing.T) {
	RequireOCIE2E(t)
	cacheDir := testCacheDir(t)
	p := newOverlayProject(t, "return { ...config, tools: {} };")

	cfg := declaredParsers(t, p, cacheDir)
	name := requireOCIParser(t, cfg)
	declared := cfg.Parsers[name]

	if declared.URL != "" {
		t.Fatalf("parser %q declares both a url and an oci source; they are mutually exclusive", name)
	}
	if len(declared.Hash) != 64 {
		t.Fatalf("parser %q hash %q is not a bare 64-hex sha256", name, declared.Hash)
	}

	res := runOnline(t, p.Dir, cacheDir, "devtools", "parsers", "list")
	if res.ExitCode != 0 {
		t.Fatalf("`devtools parsers list` against %s@%s: exit %d\nstderr:\n%s",
			declared.OCI.Ref, declared.OCI.Digest, res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, name) {
		t.Errorf("`devtools parsers list` did not report the %q module:\n%s", name, res.Stdout)
	}
}

// TestOCIParserOfflineAfterSeed is the only end-to-end proof of the airgap
// claim: seed the store while online, then run the same parser work with
// DATAMITSU_OFFLINE=1, where every network path is refused at the transport. If
// the module did not actually land in the store — or landed under a key the
// resolver does not compute — this fails instead of silently degrading.
func TestOCIParserOfflineAfterSeed(t *testing.T) {
	RequireOCIE2E(t)
	cacheDir := testCacheDir(t)
	p := newOverlayProject(t, "return { ...config, tools: {} };")

	cfg := declaredParsers(t, p, cacheDir)
	name := requireOCIParser(t, cfg)

	if res := runOnline(t, p.Dir, cacheDir, "devtools", "parsers", "prefetch"); res.ExitCode != 0 {
		t.Fatalf("`devtools parsers prefetch`: exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	// clitest.Run forces DATAMITSU_OFFLINE=1 and DATAMITSU_NO_OCI=1, which is
	// exactly the environment being asserted: the module has to come off disk.
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir}, "devtools", "parsers", "list")
	if res.ExitCode != 0 {
		t.Fatalf("`devtools parsers list` offline after prefetch: exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, name) {
		t.Errorf("offline run did not report the %q module:\n%s", name, res.Stdout)
	}
}
