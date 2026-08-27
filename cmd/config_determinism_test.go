package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"

	"go.uber.org/zap/zapcore"
)

// isolateCacheTree points the cache tree at a temp directory and returns it, so
// a test can assert on what config loading did or did not store.
//
// DATAMITSU_CACHE_DIR, not XDG_CACHE_HOME: it wins over XDG in env.getBasePath,
// so a developer running the suite inside a `datamitsu source` shell (which sets
// it) would otherwise write into their real cache and assert on an empty temp
// tree — every "stored nothing" assertion would pass vacuously.
func isolateCacheTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", dir)
	if base := env.GetCachePath(); !strings.HasPrefix(base, dir) {
		t.Fatalf("cache tree not isolated: GetCachePath() = %q, want a path under %q", base, dir)
	}
	return dir
}

// configEvalArtifactCount counts everything under the config-eval cache tree of
// an isolateCacheTree directory.
func configEvalArtifactCount(t *testing.T, cacheHome string) int {
	t.Helper()
	root := filepath.Join(cacheHome, "cache", "config-eval")
	count := 0
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", root, err)
	}
	return count
}

func writeStandaloneConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "datamitsu.config.js")
	writeFile(t, path, `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
`+body+`
}`)
	return path
}

// A config that reads the clock or the random source is not a function of the
// cache key, and one that prints cannot have its output reproduced from an
// artifact. Storing either would serve one moment's answer forever, with no
// error and no external symptom — the one failure mode of this cache a user
// could never diagnose — so the loader must refuse to store it.
func TestConfigEvalCacheableRefusesNonDeterministicConfig(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantCacheable bool
	}{
		{
			name:          "Date.now",
			body:          `var stamp = Date.now(); return { ignoreRules: ["stamped-" + stamp + ": eslint"] };`,
			wantCacheable: false,
		},
		{
			name:          "new Date",
			body:          `var year = new Date().getFullYear(); return { ignoreRules: ["year-" + year + ": eslint"] };`,
			wantCacheable: false,
		},
		{
			name:          "Math.random",
			body:          `var r = Math.random(); return { ignoreRules: ["r-" + (r < 2) + ": eslint"] };`,
			wantCacheable: false,
		},
		{
			// Not a non-deterministic read, but it costs the entry for the same
			// reason: a hit runs no JS, so a config that printed would print
			// exactly once and then fall silent forever.
			name:          "console output",
			body:          `console.log("evaluating"); return { ignoreRules: ["logged: eslint"] };`,
			wantCacheable: false,
		},
		{
			name:          "deterministic",
			body:          `return { ignoreRules: ["fixed-" + new Date(2020, 0, 1).getFullYear() + ": eslint"] };`,
			wantCacheable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cacheHome := isolateCacheTree(t)
			path := writeStandaloneConfig(t, tt.body)

			if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{path}); err != nil {
				t.Fatalf("loadConfigWithPaths: %v", err)
			}

			if got := configEvalCacheable(); got != tt.wantCacheable {
				t.Errorf("configEvalCacheable() = %v, want %v", got, tt.wantCacheable)
			}
			if !tt.wantCacheable {
				if n := configEvalArtifactCount(t, cacheHome); n != 0 {
					t.Errorf("stored %d config-eval artifacts for a non-deterministic config, want 0", n)
				}
			}
		})
	}
}

// The refusal must name its source, or a config that silently never caches is
// undiagnosable in the other direction.
func TestConfigEvalRefusalIsLogged(t *testing.T) {
	isolateCacheTree(t)
	observed := swapLoggerWithObserver(t, zapcore.DebugLevel)
	path := writeStandaloneConfig(t, `var r = Math.random(); return { ignoreRules: ["r-" + (r < 2) + ": eslint"] };`)

	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{path}); err != nil {
		t.Fatalf("loadConfigWithPaths: %v", err)
	}

	entries := observed.FilterMessageSnippet("not reproducible from the cache key").All()
	if len(entries) == 0 {
		t.Fatal("no debug entry naming the source of the refusal")
	}
	if src, ok := entries[0].ContextMap()["source"].(string); !ok || src == "" {
		t.Errorf("refusal entry has no source field: %v", entries[0].ContextMap())
	}
}

// A non-deterministic read anywhere in the chain makes the merged result
// non-deterministic, even when the last layer is pure.
func TestConfigEvalCacheableIsChainWide(t *testing.T) {
	isolateCacheTree(t)

	dirty := writeStandaloneConfig(t, `var r = Math.random(); return { ignoreRules: ["r-" + (r < 2) + ": eslint"] };`)
	clean := writeStandaloneConfig(t, `return { ignoreRules: (input && input.ignoreRules) || [] };`)

	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{dirty, clean}); err != nil {
		t.Fatalf("loadConfigWithPaths: %v", err)
	}

	if configEvalCacheable() {
		t.Error("configEvalCacheable() = true, want false: an earlier layer read Math.random")
	}
}

// The shims must not change what the config computes: a config that stamps
// itself with Date.now still evaluates, it only loses its cache entry.
func TestNonDeterministicConfigStillEvaluates(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t,
		`var year = new Date().getFullYear();
		 if (typeof year !== "number" || year < 2020) { throw new Error("clock unusable: " + year); }
		 return { ignoreRules: ["ok: eslint"] };`)

	cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{path})
	if err != nil {
		t.Fatalf("loadConfigWithPaths: %v", err)
	}
	if len(cfg.IgnoreRules) == 0 || cfg.IgnoreRules[len(cfg.IgnoreRules)-1] != "ok: eslint" {
		t.Errorf("ignoreRules = %v, want the config's own rule last", cfg.IgnoreRules)
	}
}

// A load that fails part-way must not leave the previous load's verdict
// standing: the next reader would then store a config that was never evaluated
// under that verdict.
func TestConfigEvalCacheableResetsOnFailedLoad(t *testing.T) {
	isolateCacheTree(t)

	good := writeStandaloneConfig(t, `return { ignoreRules: ["ok: eslint"] };`)
	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{good}); err != nil {
		t.Fatalf("loadConfigWithPaths: %v", err)
	}
	if !configEvalCacheable() {
		t.Fatal("deterministic config is not cacheable")
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist.js")
	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{missing}); err == nil {
		t.Fatal("loading a missing config succeeded")
	}
	if configEvalCacheable() {
		t.Error("configEvalCacheable() = true after a failed load, want false")
	}
}
