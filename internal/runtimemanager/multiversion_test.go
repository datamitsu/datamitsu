package runtimemanager

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// makeMultiVersionConfig creates a realistic config with two ESLint versions
// (eslint v10 and eslint-legacy v9) plus tools that reference them on different globs.
func makeMultiVersionConfig() (config.MapOfRuntimes, binmanager.MapOfApps, config.MapOfTools) {
	runtimes := makeTestRuntimes()

	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "10.0.0",

				BinPath: "node_modules/.bin/eslint",
				Runtime: "node",
			},
		},
		"eslint-legacy": {
			Required: true,
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "9.0.0",

				BinPath: "node_modules/.bin/eslint",
				Runtime: "node",
				Dependencies: map[string]string{
					"eslint-plugin-vue": "9.0.0",
				},
			},
		},
	}

	tools := config.MapOfTools{
		"eslint-modern": {
			Name:         "ESLint v10",
			ProjectTypes: []string{"npm-package"},
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:   "eslint",
					Args:  []string{"{files}"},
					Scope: config.ToolScopePerProject,
					Globs: []string{"src/**/*.js", "src/**/*.ts"},
				},
			},
		},
		"eslint-legacy": {
			Name:         "ESLint v9 (Legacy)",
			ProjectTypes: []string{"npm-package"},
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:   "eslint-legacy",
					Args:  []string{"{files}"},
					Scope: config.ToolScopePerProject,
					Globs: []string{"old-module/**/*.js"},
				},
			},
		},
	}

	return runtimes, apps, tools
}

func TestMultiVersionIsolatedCachePaths(t *testing.T) {
	runtimes, apps, _ := makeMultiVersionConfig()
	rm := New(runtimes)

	eslintConfig := apps["eslint"].Node
	legacyConfig := apps["eslint-legacy"].Node

	eslintPath, err := rm.GetAppPath(
		"eslint", config.RuntimeKindNode,
		eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime,
		NodeAppPathExtra{PackageName: eslintConfig.PackageName, BinPath: eslintConfig.BinPath},
	)
	if err != nil {
		t.Fatalf("GetAppPath(eslint) error = %v", err)
	}

	legacyPath, err := rm.GetAppPath(
		"eslint-legacy", config.RuntimeKindNode,
		legacyConfig.Version, legacyConfig.Dependencies, "", nil, nil, legacyConfig.Runtime,
		NodeAppPathExtra{PackageName: legacyConfig.PackageName, BinPath: legacyConfig.BinPath},
	)
	if err != nil {
		t.Fatalf("GetAppPath(eslint-legacy) error = %v", err)
	}

	if eslintPath == legacyPath {
		t.Errorf("eslint and eslint-legacy should have different cache paths:\n  eslint:        %s\n  eslint-legacy: %s", eslintPath, legacyPath)
	}

	t.Run("paths contain node runtime kind", func(t *testing.T) {
		if !strings.Contains(eslintPath, "/node/") {
			t.Errorf("eslint path should contain /node/: %s", eslintPath)
		}
		if !strings.Contains(legacyPath, "/node/") {
			t.Errorf("eslint-legacy path should contain /node/: %s", legacyPath)
		}
	})

	t.Run("paths contain app names", func(t *testing.T) {
		if !strings.Contains(eslintPath, "/eslint/") {
			t.Errorf("eslint path should contain /eslint/: %s", eslintPath)
		}
		if !strings.Contains(legacyPath, "/eslint-legacy/") {
			t.Errorf("eslint-legacy path should contain /eslint-legacy/: %s", legacyPath)
		}
	})

	t.Run("paths end with hash directories", func(t *testing.T) {
		eslintHash := filepath.Base(eslintPath)
		legacyHash := filepath.Base(legacyPath)

		if len(eslintHash) != 32 {
			t.Errorf("eslint hash length = %d, want 32", len(eslintHash))
		}
		if len(legacyHash) != 32 {
			t.Errorf("eslint-legacy hash length = %d, want 32", len(legacyHash))
		}
		if eslintHash == legacyHash {
			t.Error("hashes should differ between versions")
		}
	})
}

func TestMultiVersionToolToAppMapping(t *testing.T) {
	_, apps, tools := makeMultiVersionConfig()

	t.Run("eslint-modern tool references eslint app", func(t *testing.T) {
		tool := tools["eslint-modern"]
		lintOp := tool.Operations[config.OpLint]
		appName := lintOp.App

		app, ok := apps[appName]
		if !ok {
			t.Fatalf("tool app %q not found in apps", appName)
		}
		if app.Node == nil {
			t.Fatal("eslint app should be a node app")
		}
		if app.Node.Version != "10.0.0" {
			t.Errorf("eslint app version = %q, want 10.0.0", app.Node.Version)
		}
	})

	t.Run("eslint-legacy tool references eslint-legacy app", func(t *testing.T) {
		tool := tools["eslint-legacy"]
		lintOp := tool.Operations[config.OpLint]
		appName := lintOp.App

		app, ok := apps[appName]
		if !ok {
			t.Fatalf("tool app %q not found in apps", appName)
		}
		if app.Node == nil {
			t.Fatal("eslint-legacy app should be a node app")
		}
		if app.Node.Version != "9.0.0" {
			t.Errorf("eslint-legacy app version = %q, want 9.0.0", app.Node.Version)
		}
	})

	t.Run("tools operate on different globs", func(t *testing.T) {
		modernGlobs := tools["eslint-modern"].Operations[config.OpLint].Globs
		legacyGlobs := tools["eslint-legacy"].Operations[config.OpLint].Globs

		modernMatch := false
		for _, g := range modernGlobs {
			if strings.HasPrefix(g, "src/") {
				modernMatch = true
			}
		}
		if !modernMatch {
			t.Errorf("modern tool should target src/ files, got globs: %v", modernGlobs)
		}

		legacyMatch := false
		for _, g := range legacyGlobs {
			if strings.HasPrefix(g, "old-module/") {
				legacyMatch = true
			}
		}
		if !legacyMatch {
			t.Errorf("legacy tool should target old-module/ files, got globs: %v", legacyGlobs)
		}
	})
}

func TestMultiVersionCacheKeyStability(t *testing.T) {
	runtimes, apps, _ := makeMultiVersionConfig()
	rm := New(runtimes)

	eslintConfig := apps["eslint"].Node
	legacyConfig := apps["eslint-legacy"].Node

	eslintExtra := NodeAppPathExtra{PackageName: eslintConfig.PackageName, BinPath: eslintConfig.BinPath}
	legacyExtra := NodeAppPathExtra{PackageName: legacyConfig.PackageName, BinPath: legacyConfig.BinPath}

	t.Run("same config produces same path across calls", func(t *testing.T) {
		path1, _ := rm.GetAppPath("eslint", config.RuntimeKindNode,
			eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)
		path2, _ := rm.GetAppPath("eslint", config.RuntimeKindNode,
			eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)

		if path1 != path2 {
			t.Errorf("paths not stable: %q != %q", path1, path2)
		}
	})

	t.Run("version change produces new path", func(t *testing.T) {
		pathOriginal, _ := rm.GetAppPath("eslint", config.RuntimeKindNode,
			eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)

		pathNewVersion, _ := rm.GetAppPath("eslint", config.RuntimeKindNode,
			"10.1.0", eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)

		if pathOriginal == pathNewVersion {
			t.Error("different versions should produce different paths")
		}
	})

	t.Run("dependency change produces new path", func(t *testing.T) {
		pathOriginal, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindNode,
			legacyConfig.Version, legacyConfig.Dependencies, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		modifiedDeps := map[string]string{
			"eslint-plugin-vue": "10.0.0",
		}
		pathNewDeps, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindNode,
			legacyConfig.Version, modifiedDeps, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		if pathOriginal == pathNewDeps {
			t.Error("different dependencies should produce different paths")
		}
	})

	t.Run("adding dependency produces new path", func(t *testing.T) {
		pathOriginal, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindNode,
			legacyConfig.Version, legacyConfig.Dependencies, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		extraDeps := map[string]string{
			"eslint-plugin-vue":    "9.0.0",
			"eslint-plugin-import": "2.29.0",
		}
		pathExtraDep, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindNode,
			legacyConfig.Version, extraDeps, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		if pathOriginal == pathExtraDep {
			t.Error("adding a dependency should produce a different path")
		}
	})
}

func TestMultiVersionRuntimeCollectsForBothApps(t *testing.T) {
	runtimes, apps, _ := makeMultiVersionConfig()

	collected := CollectRequiredRuntimes(apps, runtimes, false)

	found := slices.Contains(collected, "node")
	if !found {
		t.Errorf("expected node runtime to be collected, got %v", collected)
	}

	if len(collected) != 1 {
		t.Errorf("expected exactly 1 runtime (node) for both eslint apps, got %d: %v",
			len(collected), collected)
	}
}
