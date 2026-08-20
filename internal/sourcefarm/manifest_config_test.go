package sourcefarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// mustRemove deletes a file that a test needs gone, so a missing-file transition
// is a real one rather than an mtime coincidence.
func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
}

// configFixture lays out a config chain in a directory that is deliberately not
// a repository — no .git anywhere — and bakes an explicit-config manifest over
// it, the way activation outside a repository will.
func configFixture(t *testing.T) (chain []string, m Manifest) {
	t.Helper()

	dir := t.TempDir()
	main := filepath.Join(dir, "datamitsu.config.ts")
	before := filepath.Join(dir, "before.config.ts")
	mustWrite(t, main, "export const getConfig = () => ({})\n")
	mustWrite(t, before, "export default {}\n")
	chain = []string{main, before}

	plan := Plan{
		FarmDir: filepath.Join(dir, "farm"),
		Entries: []Entry{{Name: "tofu", Kind: "binary", Strategy: StrategyShim, Command: "/store/.bin/tofu/abc", Installed: true}},
	}
	m = BuildConfigManifest(plan, chain, WatchSet(ConfigWatchPaths(chain)))
	if !Validate(m) {
		t.Fatalf("freshly built config manifest is not fresh: %+v", m)
	}
	return chain, m
}

func TestBuildConfigManifest(t *testing.T) {
	chain, m := configFixture(t)

	t.Run("records the explicit-config origin", func(t *testing.T) {
		if m.Origin != OriginExplicitConfig {
			t.Errorf("Origin = %q, want %q", m.Origin, OriginExplicitConfig)
		}
	})

	t.Run("records the config chain in order", func(t *testing.T) {
		if !slices.Equal(m.ConfigPaths, chain) {
			t.Errorf("ConfigPaths = %v, want %v", m.ConfigPaths, chain)
		}
	})

	t.Run("records no git root", func(t *testing.T) {
		if m.Root != "" {
			t.Errorf("Root = %q, want empty: an explicit-config farm has no git root", m.Root)
		}
	})

	t.Run("does not alias the caller's slice", func(t *testing.T) {
		mutated := append([]string(nil), chain...)
		built := BuildConfigManifest(Plan{}, mutated, nil)
		mutated[0] = "/somewhere/else"
		if built.ConfigPaths[0] == "/somewhere/else" {
			t.Error("BuildConfigManifest kept a reference to the caller's slice")
		}
	})

	t.Run("round-trips through JSON", func(t *testing.T) {
		data, err := Encode(m)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		var decoded Manifest
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if decoded.Origin != OriginExplicitConfig {
			t.Errorf("decoded Origin = %q, want %q", decoded.Origin, OriginExplicitConfig)
		}
		if !slices.Equal(decoded.ConfigPaths, chain) {
			t.Errorf("decoded ConfigPaths = %v, want %v", decoded.ConfigPaths, chain)
		}
	})

	t.Run("a git-root manifest carries no config paths", func(t *testing.T) {
		plain := BuildManifest(Plan{Root: "/repo"}, OriginGitRoot, nil)
		if plain.ConfigPaths != nil {
			t.Errorf("ConfigPaths = %v, want nil for a git-root farm", plain.ConfigPaths)
		}
		data, err := Encode(plain)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if got := string(data); strings.Contains(got, "configPaths") {
			t.Errorf("git-root manifest serialized configPaths:\n%s", got)
		}
	})
}

func TestConfigWatchPaths(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "datamitsu.config.ts")
	before := filepath.Join(dir, "before.config.ts")

	got := ConfigWatchPaths([]string{main, before})

	t.Run("watches exactly the chain", func(t *testing.T) {
		want := []string{main, before}
		if !slices.Equal(got, want) {
			t.Errorf("ConfigWatchPaths() = %v, want %v", got, want)
		}
	})

	t.Run("invents no repository tripwires", func(t *testing.T) {
		for _, p := range got {
			base := filepath.Base(p)
			if base == "HEAD" || base == "pnpm-lock.yaml" {
				t.Errorf("ConfigWatchPaths() watches %q; there is no repository behind a config farm", p)
			}
		}
	})
}

// TestValidate_ConfigFarmStaleness covers the transitions a config farm must
// notice, and the one it must not: a repository the shell happens to be in
// changing has no bearing on a machine-level farm.
func TestValidate_ConfigFarmStaleness(t *testing.T) {
	t.Run("a changed config file makes the farm stale", func(t *testing.T) {
		chain, m := configFixture(t)
		touchWithMtime(t, chain[0], "export const getConfig = () => ({apps: {}})\n", time.Now().Add(time.Hour))
		if Validate(m) {
			t.Error("Validate() = true after the config changed, want false")
		}
	})

	t.Run("a deleted config file makes the farm stale", func(t *testing.T) {
		chain, m := configFixture(t)
		mustRemove(t, chain[1])
		if Validate(m) {
			t.Error("Validate() = true after a chain file was deleted, want false")
		}
	})

	t.Run("an untouched chain stays fresh", func(t *testing.T) {
		_, m := configFixture(t)
		if !Validate(m) {
			t.Error("Validate() = false with the chain untouched, want true")
		}
	})
}
