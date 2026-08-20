package env

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeConfig creates a config file and returns its path.
func writeConfig(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("export function getConfig() { return {} }\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestConfigFarmIdentity(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

	t.Run("different config paths produce different identities", func(t *testing.T) {
		dir := t.TempDir()
		a := writeConfig(t, dir, "a.config.ts")
		b := writeConfig(t, dir, "b.config.ts")

		idA, err := ConfigFarmIdentity([]string{a})
		if err != nil {
			t.Fatalf("ConfigFarmIdentity(a) error = %v", err)
		}
		idB, err := ConfigFarmIdentity([]string{b})
		if err != nil {
			t.Fatalf("ConfigFarmIdentity(b) error = %v", err)
		}
		if idA == idB {
			t.Errorf("distinct configs share identity %q", idA)
		}
	})

	t.Run("chain order is significant", func(t *testing.T) {
		dir := t.TempDir()
		a := writeConfig(t, dir, "a.config.ts")
		b := writeConfig(t, dir, "b.config.ts")

		forward, err := ConfigFarmIdentity([]string{a, b})
		if err != nil {
			t.Fatalf("ConfigFarmIdentity(a, b) error = %v", err)
		}
		reverse, err := ConfigFarmIdentity([]string{b, a})
		if err != nil {
			t.Fatalf("ConfigFarmIdentity(b, a) error = %v", err)
		}
		if forward == reverse {
			t.Error("reordering the chain kept the identity; merge order changes the effective config")
		}
	})

	t.Run("relative, absolute and symlinked spellings are one identity", func(t *testing.T) {
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatalf("EvalSymlinks(TempDir) error = %v", err)
		}
		abs := writeConfig(t, dir, "datamitsu.config.ts")

		link := filepath.Join(dir, "link.config.ts")
		if err := os.Symlink(abs, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		t.Chdir(dir)

		want, err := ConfigFarmIdentity([]string{abs})
		if err != nil {
			t.Fatalf("ConfigFarmIdentity(abs) error = %v", err)
		}
		for _, spelling := range []string{"./datamitsu.config.ts", "datamitsu.config.ts", link, "./link.config.ts"} {
			got, err := ConfigFarmIdentity([]string{spelling})
			if err != nil {
				t.Fatalf("ConfigFarmIdentity(%q) error = %v", spelling, err)
			}
			if got != want {
				t.Errorf("ConfigFarmIdentity(%q) = %q, want %q", spelling, got, want)
			}
		}
	})

	t.Run("identity is stable across calls", func(t *testing.T) {
		dir := t.TempDir()
		cfg := writeConfig(t, dir, "datamitsu.config.ts")
		first, err := ConfigFarmIdentity([]string{cfg})
		if err != nil {
			t.Fatalf("ConfigFarmIdentity() error = %v", err)
		}
		for range 3 {
			again, err := ConfigFarmIdentity([]string{cfg})
			if err != nil {
				t.Fatalf("ConfigFarmIdentity() error = %v", err)
			}
			if again != first {
				t.Fatalf("identity changed between calls: %q then %q", first, again)
			}
		}
	})

	t.Run("resolving an already-resolved chain is idempotent", func(t *testing.T) {
		dir := t.TempDir()
		cfg := writeConfig(t, dir, "datamitsu.config.ts")
		resolved, err := ResolveConfigChain([]string{cfg})
		if err != nil {
			t.Fatalf("ResolveConfigChain() error = %v", err)
		}
		twice, err := ResolveConfigChain(resolved)
		if err != nil {
			t.Fatalf("ResolveConfigChain(resolved) error = %v", err)
		}
		if len(twice) != len(resolved) || twice[0] != resolved[0] {
			t.Errorf("ResolveConfigChain not idempotent: %v then %v", resolved, twice)
		}
	})

	t.Run("a nonexistent path still resolves to a stable absolute name", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(dir, "nope.config.ts")
		resolved, err := ResolveConfigChain([]string{missing})
		if err != nil {
			t.Fatalf("ResolveConfigChain() error = %v", err)
		}
		if len(resolved) != 1 || !filepath.IsAbs(resolved[0]) {
			t.Fatalf("ResolveConfigChain(%q) = %v, want one absolute path", missing, resolved)
		}
	})

	t.Run("empty inputs are errors, not silent identities", func(t *testing.T) {
		if _, err := ConfigFarmIdentity(nil); err == nil {
			t.Error("ConfigFarmIdentity(nil) error = nil, want an error")
		}
		if _, err := ConfigFarmIdentity([]string{""}); err == nil {
			t.Error("ConfigFarmIdentity([\"\"]) error = nil, want an error")
		}
	})
}

func TestGetConfigFarmBinPath(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

	dir := t.TempDir()
	cfg := writeConfig(t, dir, "datamitsu.config.ts")

	t.Run("path shape and location", func(t *testing.T) {
		got, err := GetConfigFarmBinPath([]string{cfg})
		if err != nil {
			t.Fatalf("GetConfigFarmBinPath() error = %v", err)
		}
		identity, err := ConfigFarmIdentity([]string{cfg})
		if err != nil {
			t.Fatalf("ConfigFarmIdentity() error = %v", err)
		}
		want := filepath.Join(GetCachePath(), ConfigFarmsDirName, identity, "bin")
		if got != want {
			t.Errorf("GetConfigFarmBinPath() = %q, want %q", got, want)
		}
	})

	t.Run("path is clean and escapes nothing", func(t *testing.T) {
		got, err := GetConfigFarmBinPath([]string{cfg})
		if err != nil {
			t.Fatalf("GetConfigFarmBinPath() error = %v", err)
		}
		if got != filepath.Clean(got) {
			t.Errorf("GetConfigFarmBinPath() = %q is not clean", got)
		}
		if strings.Contains(got, "..") {
			t.Errorf("GetConfigFarmBinPath() = %q contains ..", got)
		}
		if !strings.HasPrefix(got, filepath.Clean(GetCachePath())+string(filepath.Separator)) {
			t.Errorf("GetConfigFarmBinPath() = %q is outside the cache root %q", got, GetCachePath())
		}
	})

	t.Run("manifest is a sibling of the farm directory", func(t *testing.T) {
		binPath, err := GetConfigFarmBinPath([]string{cfg})
		if err != nil {
			t.Fatalf("GetConfigFarmBinPath() error = %v", err)
		}
		manifest, err := GetConfigFarmManifestPath([]string{cfg})
		if err != nil {
			t.Fatalf("GetConfigFarmManifestPath() error = %v", err)
		}
		if filepath.Dir(binPath) != filepath.Dir(manifest) {
			t.Errorf("manifest %q is not a sibling of bin dir %q", manifest, binPath)
		}
		if filepath.Base(manifest) != ProjectManifestFileName {
			t.Errorf("manifest base = %q, want %q", filepath.Base(manifest), ProjectManifestFileName)
		}
	})

	t.Run("a config farm never collides with a project farm", func(t *testing.T) {
		// Same directory on both sides: a project farm for dir, and a config farm
		// for the config that lives in it. The namespaces are what keep them
		// apart, so the identities are allowed to be anything.
		project, err := GetProjectBinPath(dir)
		if err != nil {
			t.Fatalf("GetProjectBinPath() error = %v", err)
		}
		config, err := GetConfigFarmBinPath([]string{cfg})
		if err != nil {
			t.Fatalf("GetConfigFarmBinPath() error = %v", err)
		}
		if project == config {
			t.Fatalf("project and config farms share the path %q", project)
		}
		projectNS := filepath.Dir(filepath.Dir(filepath.Dir(project)))
		configNS := filepath.Dir(filepath.Dir(filepath.Dir(config)))
		if projectNS != configNS {
			t.Fatalf("farms are not siblings under one cache root: %q vs %q", projectNS, configNS)
		}
		if filepath.Base(filepath.Dir(filepath.Dir(config))) != ConfigFarmsDirName {
			t.Errorf("config farm is not under %q: %q", ConfigFarmsDirName, config)
		}

		// The stronger claim: even a config farm whose identity hash happened to
		// equal a project's could not land on the same path, because the
		// namespace element differs.
		sameHash := filepath.Join(GetCachePath(), "projects", HashProjectPath(dir))
		if strings.HasPrefix(config, sameHash+string(filepath.Separator)) {
			t.Errorf("config farm %q landed inside the project namespace %q", config, sameHash)
		}
	})

	t.Run("relative and absolute spellings name one farm", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("chdir-based spelling test is exercised on POSIX only")
		}
		resolvedDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatalf("EvalSymlinks() error = %v", err)
		}
		t.Chdir(resolvedDir)

		absFarm, err := GetConfigFarmBinPath([]string{filepath.Join(resolvedDir, "datamitsu.config.ts")})
		if err != nil {
			t.Fatalf("GetConfigFarmBinPath(abs) error = %v", err)
		}
		relFarm, err := GetConfigFarmBinPath([]string{"./datamitsu.config.ts"})
		if err != nil {
			t.Fatalf("GetConfigFarmBinPath(rel) error = %v", err)
		}
		if absFarm != relFarm {
			t.Errorf("relative spelling produced a second farm: %q vs %q", relFarm, absFarm)
		}
	})
}
