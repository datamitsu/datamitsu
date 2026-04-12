package runtimemanager

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"path/filepath"
	"strings"
	"testing"
)

// makeMultiVersionConfig creates a realistic config with two ESLint versions
// (eslint v10 and eslint-legacy v9) plus tools that reference them on different globs.
func makeMultiVersionConfig() (config.MapOfRuntimes, binmanager.MapOfApps, config.MapOfTools) {
	runtimes := makeTestRuntimes()

	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "eslint",
				Version:     "10.0.0",

				BinPath:     "node_modules/.bin/eslint",
				Runtime:     "fnm",
			},
		},
		"eslint-legacy": {
			Required: true,
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "eslint",
				Version:     "9.0.0",

				BinPath:     "node_modules/.bin/eslint",
				Runtime:     "fnm",
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

	eslintConfig := apps["eslint"].Fnm
	legacyConfig := apps["eslint-legacy"].Fnm

	eslintPath, err := rm.GetAppPath(
		"eslint", config.RuntimeKindFNM,
		eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime,
		FNMAppPathExtra{PackageName: eslintConfig.PackageName, BinPath: eslintConfig.BinPath},
	)
	if err != nil {
		t.Fatalf("GetAppPath(eslint) error = %v", err)
	}

	legacyPath, err := rm.GetAppPath(
		"eslint-legacy", config.RuntimeKindFNM,
		legacyConfig.Version, legacyConfig.Dependencies, "", nil, nil, legacyConfig.Runtime,
		FNMAppPathExtra{PackageName: legacyConfig.PackageName, BinPath: legacyConfig.BinPath},
	)
	if err != nil {
		t.Fatalf("GetAppPath(eslint-legacy) error = %v", err)
	}

	if eslintPath == legacyPath {
		t.Errorf("eslint and eslint-legacy should have different cache paths:\n  eslint:        %s\n  eslint-legacy: %s", eslintPath, legacyPath)
	}

	t.Run("paths contain fnm runtime kind", func(t *testing.T) {
		if !strings.Contains(eslintPath, "/fnm/") {
			t.Errorf("eslint path should contain /fnm/: %s", eslintPath)
		}
		if !strings.Contains(legacyPath, "/fnm/") {
			t.Errorf("eslint-legacy path should contain /fnm/: %s", legacyPath)
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

func TestMultiVersionIsolatedCommandInfo(t *testing.T) {
	runtimes, apps, _ := makeMultiVersionConfig()
	rm := New(runtimes)

	eslintConfig := apps["eslint"].Fnm
	legacyConfig := apps["eslint-legacy"].Fnm

	eslintInfo, err := rm.GetFNMCommandInfo("eslint", eslintConfig, nil, nil)
	if err != nil {
		t.Fatalf("GetFNMCommandInfo(eslint) error = %v", err)
	}
	legacyInfo, err := rm.GetFNMCommandInfo("eslint-legacy", legacyConfig, nil, nil)
	if err != nil {
		t.Fatalf("GetFNMCommandInfo(eslint-legacy) error = %v", err)
	}

	t.Run("both point to app binaries", func(t *testing.T) {
		if filepath.Base(eslintInfo.Command) != "eslint" {
			t.Errorf("eslint command name = %q, want 'eslint'", filepath.Base(eslintInfo.Command))
		}
		if filepath.Base(legacyInfo.Command) != "eslint" {
			t.Errorf("eslint-legacy command name = %q, want 'eslint'", filepath.Base(legacyInfo.Command))
		}
	})

	t.Run("different versions have different command paths", func(t *testing.T) {
		if eslintInfo.Command == legacyInfo.Command {
			t.Error("different app versions should have different command paths")
		}
	})

	t.Run("PATH env includes node binary directory", func(t *testing.T) {
		eslintPath, ok := eslintInfo.Env["PATH"]
		if !ok {
			t.Fatal("eslint PATH not set")
		}
		legacyPath, ok := legacyInfo.Env["PATH"]
		if !ok {
			t.Fatal("eslint-legacy PATH not set")
		}
		if !strings.Contains(eslintPath, "fnm-nodes") {
			t.Errorf("eslint PATH should contain fnm-nodes, got %q", eslintPath)
		}
		if !strings.Contains(legacyPath, "fnm-nodes") {
			t.Errorf("eslint-legacy PATH should contain fnm-nodes, got %q", legacyPath)
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
		if app.Fnm == nil {
			t.Fatal("eslint app should be an fnm app")
		}
		if app.Fnm.Version != "10.0.0" {
			t.Errorf("eslint app version = %q, want 10.0.0", app.Fnm.Version)
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
		if app.Fnm == nil {
			t.Fatal("eslint-legacy app should be an fnm app")
		}
		if app.Fnm.Version != "9.0.0" {
			t.Errorf("eslint-legacy app version = %q, want 9.0.0", app.Fnm.Version)
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

func TestMultiVersionBinManagerDelegation(t *testing.T) {
	runtimes, apps, _ := makeMultiVersionConfig()
	rm := New(runtimes)

	var eslintCmdInfo *binmanager.CommandInfo
	var legacyCmdInfo *binmanager.CommandInfo

	mock := &mockRuntimeAppManagerForMultiVersion{rm: rm}

	bm := binmanager.New(apps, nil, mock)

	t.Run("eslint routes to v10 through BinManager", func(t *testing.T) {
		info, err := bm.GetCommandInfo("eslint")
		if err != nil {
			t.Fatalf("GetCommandInfo(eslint) error = %v", err)
		}
		if info.Type != "fnm" {
			t.Errorf("type = %q, want 'fnm'", info.Type)
		}
		eslintCmdInfo = info
	})

	t.Run("eslint-legacy routes to v9 through BinManager", func(t *testing.T) {
		info, err := bm.GetCommandInfo("eslint-legacy")
		if err != nil {
			t.Fatalf("GetCommandInfo(eslint-legacy) error = %v", err)
		}
		if info.Type != "fnm" {
			t.Errorf("type = %q, want 'fnm'", info.Type)
		}
		legacyCmdInfo = info
	})

	t.Run("delegated app commands are different", func(t *testing.T) {
		if eslintCmdInfo == nil || legacyCmdInfo == nil {
			t.Skip("previous subtests failed")
		}
		if eslintCmdInfo.Command == legacyCmdInfo.Command {
			t.Errorf("BinManager should delegate to different app binary paths:\n  eslint:        %s\n  eslint-legacy: %s",
				eslintCmdInfo.Command, legacyCmdInfo.Command)
		}
	})
}

// mockRuntimeAppManagerForMultiVersion uses a real RuntimeManager to return
// command info without performing actual installation.
type mockRuntimeAppManagerForMultiVersion struct {
	rm *RuntimeManager
}

func (m *mockRuntimeAppManagerForMultiVersion) GetCommandInfo(appName string, app binmanager.App) (*binmanager.CommandInfo, error) {
	if app.Fnm != nil {
		return m.rm.GetFNMCommandInfo(appName, app.Fnm, app.Files, nil)
	}
	if app.Uv != nil {
		return m.rm.GetUVCommandInfo(appName, app.Uv, app.Files, nil)
	}
	return nil, nil
}

func (m *mockRuntimeAppManagerForMultiVersion) ComputeAppPath(appName string, app binmanager.App) (string, error) {
	return m.rm.ComputeAppPath(appName, app)
}

func TestMultiVersionCacheKeyStability(t *testing.T) {
	runtimes, apps, _ := makeMultiVersionConfig()
	rm := New(runtimes)

	eslintConfig := apps["eslint"].Fnm
	legacyConfig := apps["eslint-legacy"].Fnm

	eslintExtra := FNMAppPathExtra{PackageName: eslintConfig.PackageName, BinPath: eslintConfig.BinPath}
	legacyExtra := FNMAppPathExtra{PackageName: legacyConfig.PackageName, BinPath: legacyConfig.BinPath}

	t.Run("same config produces same path across calls", func(t *testing.T) {
		path1, _ := rm.GetAppPath("eslint", config.RuntimeKindFNM,
			eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)
		path2, _ := rm.GetAppPath("eslint", config.RuntimeKindFNM,
			eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)

		if path1 != path2 {
			t.Errorf("paths not stable: %q != %q", path1, path2)
		}
	})

	t.Run("version change produces new path", func(t *testing.T) {
		pathOriginal, _ := rm.GetAppPath("eslint", config.RuntimeKindFNM,
			eslintConfig.Version, eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)

		pathNewVersion, _ := rm.GetAppPath("eslint", config.RuntimeKindFNM,
			"10.1.0", eslintConfig.Dependencies, "", nil, nil, eslintConfig.Runtime, eslintExtra)

		if pathOriginal == pathNewVersion {
			t.Error("different versions should produce different paths")
		}
	})

	t.Run("dependency change produces new path", func(t *testing.T) {
		pathOriginal, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindFNM,
			legacyConfig.Version, legacyConfig.Dependencies, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		modifiedDeps := map[string]string{
			"eslint-plugin-vue": "10.0.0",
		}
		pathNewDeps, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindFNM,
			legacyConfig.Version, modifiedDeps, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		if pathOriginal == pathNewDeps {
			t.Error("different dependencies should produce different paths")
		}
	})

	t.Run("adding dependency produces new path", func(t *testing.T) {
		pathOriginal, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindFNM,
			legacyConfig.Version, legacyConfig.Dependencies, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		extraDeps := map[string]string{
			"eslint-plugin-vue":    "9.0.0",
			"eslint-plugin-import": "2.29.0",
		}
		pathExtraDep, _ := rm.GetAppPath("eslint-legacy", config.RuntimeKindFNM,
			legacyConfig.Version, extraDeps, "", nil, nil, legacyConfig.Runtime, legacyExtra)

		if pathOriginal == pathExtraDep {
			t.Error("adding a dependency should produce a different path")
		}
	})
}

func TestMultiVersionSamePackageDifferentAppNames(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("same packageName different versions produce different paths", func(t *testing.T) {
		info1, err := rm.GetFNMCommandInfo("eslint", &binmanager.AppConfigFNM{
			PackageName: "eslint",
			Version:     "10.0.0",

			BinPath:     "node_modules/.bin/eslint",
			Runtime:     "fnm",
		}, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo(eslint v10) error = %v", err)
		}

		info2, err := rm.GetFNMCommandInfo("eslint-legacy", &binmanager.AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
			Runtime:     "fnm",
			Dependencies: map[string]string{
				"eslint-plugin-vue": "9.0.0",
			},
		}, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo(eslint-legacy v9) error = %v", err)
		}

		if info1.Command == info2.Command {
			t.Error("same package with different versions should have different binary paths")
		}
	})

	t.Run("same version different deps produce different paths", func(t *testing.T) {
		info1, err := rm.GetFNMCommandInfo("eslint-bare", &binmanager.AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
			Runtime:     "fnm",
		}, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo(eslint-bare) error = %v", err)
		}

		info2, err := rm.GetFNMCommandInfo("eslint-vue", &binmanager.AppConfigFNM{
			PackageName:  "eslint",
			Version:      "9.0.0",

			BinPath:      "node_modules/.bin/eslint",
			Runtime:      "fnm",
			Dependencies: map[string]string{"eslint-plugin-vue": "9.0.0"},
		}, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo(eslint-vue) error = %v", err)
		}

		if info1.Command == info2.Command {
			t.Error("same package with different deps should have different binary paths")
		}
	})
}

func TestMultiVersionRuntimeCollectsForBothApps(t *testing.T) {
	runtimes, apps, _ := makeMultiVersionConfig()

	collected := CollectRequiredRuntimes(apps, runtimes, false)

	found := false
	for _, name := range collected {
		if name == "fnm" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected fnm runtime to be collected, got %v", collected)
	}

	if len(collected) != 1 {
		t.Errorf("expected exactly 1 runtime (fnm) for both eslint apps, got %d: %v",
			len(collected), collected)
	}
}
