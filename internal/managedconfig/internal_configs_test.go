package managedconfig

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/hashutil"
)

func internalEntry(content string) config.ManagedConfig {
	return config.ManagedConfig{
		Tools:     []string{"tool"},
		Scope:     config.ScopeGitRoot,
		Ejectable: true,
		Placement: config.PlacementInternal,
		Render:    &config.ManagedConfigRender{Content: content, Hash: hashutil.XXH3Hex([]byte(content))},
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriteInternalConfigs(t *testing.T) {
	root := t.TempDir()
	configs := config.MapOfManagedConfigs{
		".gitleaks.toml":     internalEntry("a"),
		".github/zizmor.yml": internalEntry("b"),
		"eslint.config.mjs":  {Tools: []string{"eslint"}, Placement: config.PlacementRepo},
	}

	res, err := WriteInternalConfigs(root, configs, false)
	if err != nil {
		t.Fatalf("WriteInternalConfigs() = %v", err)
	}
	if !slices.Equal(res.Written, []string{".datamitsu/configs/.github/zizmor.yml", ".datamitsu/configs/.gitleaks.toml"}) {
		t.Errorf("Written = %v", res.Written)
	}
	if got := readFile(t, filepath.Join(root, ".datamitsu", "configs", ".github", "zizmor.yml")); got != "b" {
		t.Errorf("nested file = %q", got)
	}
	if got := readFile(t, filepath.Join(root, ".datamitsu", ".gitignore")); got != "*\n" {
		t.Errorf("a .datamitsu it created must ignore itself, got .gitignore %q", got)
	}

	t.Run("an unchanged set writes nothing", func(t *testing.T) {
		res, err := WriteInternalConfigs(root, configs, false)
		if err != nil || res.Changed() || len(res.Unchanged) != 2 {
			t.Fatalf("second write = %+v, %v", res, err)
		}
	})

	t.Run("a changed render is replaced and a stale file removed", func(t *testing.T) {
		stray := filepath.Join(root, ".datamitsu", "configs", "stray.yml")
		if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		next := config.MapOfManagedConfigs{".gitleaks.toml": internalEntry("a2")}
		res, err := WriteInternalConfigs(root, next, false)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(res.Written, []string{".datamitsu/configs/.gitleaks.toml"}) ||
			!slices.Equal(res.Removed, []string{".datamitsu/configs/.github/zizmor.yml", ".datamitsu/configs/stray.yml"}) {
			t.Errorf("result = %+v", res)
		}
		if got := readFile(t, filepath.Join(root, ".datamitsu", "configs", ".gitleaks.toml")); got != "a2" {
			t.Errorf("content = %q", got)
		}
		if _, err := os.Stat(stray); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stray file survived: %v", err)
		}
	})

	t.Run("dry-run reports without writing", func(t *testing.T) {
		res, err := WriteInternalConfigs(root, config.MapOfManagedConfigs{".gitleaks.toml": internalEntry("a3")}, true)
		if err != nil || len(res.Written) != 1 {
			t.Fatalf("dry-run = %+v, %v", res, err)
		}
		if got := readFile(t, filepath.Join(root, ".datamitsu", "configs", ".gitleaks.toml")); got != "a2" {
			t.Errorf("dry-run wrote %q", got)
		}
	})

	t.Run("an empty set removes the directory", func(t *testing.T) {
		if _, err := WriteInternalConfigs(root, config.MapOfManagedConfigs{}, false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, ".datamitsu", "configs")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("configs/ survived an empty set: %v", err)
		}
	})
}

func TestLinkRebuildCarriesInternalConfigsOver(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteInternalConfigs(root, config.MapOfManagedConfigs{".gitleaks.toml": internalEntry("kept")}, false); err != nil {
		t.Fatal(err)
	}

	if err := CreateDatamitsuTypeDefinitions(root, false); err != nil {
		t.Fatalf("CreateDatamitsuTypeDefinitions() = %v", err)
	}
	if got := readFile(t, filepath.Join(root, ".datamitsu", "configs", ".gitleaks.toml")); got != "kept" {
		t.Fatalf("type-definition rebuild lost configs/: %q", got)
	}

	installRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(installRoot, "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	apps := binmanager.MapOfApps{"tool": {Links: map[string]string{"tool.config.js": "index.js"}}}
	resolver := &mockResolver{paths: map[string]string{"tool": installRoot}}
	if _, err := CreateDatamitsuLinks(root, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("CreateDatamitsuLinks() = %v", err)
	}
	if got := readFile(t, filepath.Join(root, ".datamitsu", "configs", ".gitleaks.toml")); got != "kept" {
		t.Fatalf("link rebuild lost configs/: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(root, ".datamitsu", "tool.config.js")); err != nil {
		t.Errorf("link missing after rebuild: %v", err)
	}
}

func TestLinkNameConfigsIsReserved(t *testing.T) {
	installRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(installRoot, "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := &mockResolver{paths: map[string]string{"tool": installRoot}}
	for _, name := range []string{"configs", "configs/x.toml", "Configs", "CONFIGS/x.toml"} {
		for _, dryRun := range []bool{true, false} {
			apps := binmanager.MapOfApps{"tool": {Links: map[string]string{name: "index.js"}}}
			_, err := CreateDatamitsuLinks(t.TempDir(), apps, resolver, nil, nil, dryRun)
			if err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Errorf("link %q (dryRun=%v): err = %v, want reserved", name, dryRun, err)
			}
		}
	}
}

func withAlternate(mc config.ManagedConfig) config.ManagedConfig {
	mc.OtherFileNameList = []string{"gitleaks.toml"}
	return mc
}

func TestCheckConfigFiles(t *testing.T) {
	refs := []config.ManagedConfigRef{{Tool: "gitleaks", Key: ".gitleaks.toml"}}
	internal := internalEntry("rendered")
	internal.Tools = []string{"gitleaks"}
	ejected := config.ManagedConfig{Tools: []string{"gitleaks"}, Scope: config.ScopeGitRoot, Ejectable: true, Placement: config.PlacementRepo}

	write := func(t *testing.T, root, rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name  string
		entry config.ManagedConfig
		files map[string]string
		want  string
	}{
		{"internal and current", internal, map[string]string{".datamitsu/configs/.gitleaks.toml": "rendered"}, ""},
		{"internal and missing", internal, nil, "run `datamitsu init`"},
		{"internal and stale", internal, map[string]string{".datamitsu/configs/.gitleaks.toml": "old"}, "out of date"},
		{"internal with a repository copy", internal, map[string]string{
			".datamitsu/configs/.gitleaks.toml": "rendered",
			".gitleaks.toml":                    "custom",
		}, `add "gitleaks" to ejectConfigs`},
		{"ejected and present", ejected, map[string]string{".gitleaks.toml": "x"}, ""},
		{"ejected and missing", ejected, nil, "config reconcile --tools gitleaks"},
		{"not ejectable is left alone", config.ManagedConfig{Tools: []string{"gitleaks"}, Placement: config.PlacementRepo}, nil, ""},
		{"internal with a file under another name", withAlternate(internal), map[string]string{
			".datamitsu/configs/.gitleaks.toml": "rendered",
			"gitleaks.toml":                     "[allowlist]",
		}, "gitleaks.toml is another name for .gitleaks.toml"},
		{"ejected with a file under another name", withAlternate(ejected), map[string]string{
			".gitleaks.toml": "x",
			"gitleaks.toml":  "[allowlist]",
		}, "never reads (it reads .gitleaks.toml)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range tt.files {
				write(t, root, rel, content)
			}
			err := CheckConfigFiles(root, config.MapOfManagedConfigs{".gitleaks.toml": tt.entry}, refs)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("CheckConfigFiles() = %v, want nil", err)
				}
				return
			}
			var pe *PreflightError
			if !errors.As(err, &pe) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CheckConfigFiles() = %v, want a PreflightError containing %q", err, tt.want)
			}
		})
	}
}

func TestWriteInternalConfigsReplacesALinkIntoTheStore(t *testing.T) {
	root := t.TempDir()
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".datamitsu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store, filepath.Join(root, ".datamitsu", "configs")); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteInternalConfigs(root, config.MapOfManagedConfigs{".gitleaks.toml": internalEntry("a")}, false); err != nil {
		t.Fatalf("WriteInternalConfigs() = %v", err)
	}
	if entries, _ := os.ReadDir(store); len(entries) != 0 {
		t.Fatalf("wrote through the link into the store: %v", entries)
	}
	info, err := os.Lstat(filepath.Join(root, ".datamitsu", "configs"))
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("configs/ is still a link: %v, %v", info, err)
	}
	if got := readFile(t, filepath.Join(root, ".datamitsu", "configs", ".gitleaks.toml")); got != "a" {
		t.Errorf("content = %q", got)
	}
}
