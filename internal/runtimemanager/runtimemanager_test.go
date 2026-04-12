package runtimemanager

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
	"os/exec"
	"testing"
)

func makeTestRuntimes() config.MapOfRuntimes {
	return config.MapOfRuntimes{
		"uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeDarwin: {
						syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/uv-darwin-amd64.tar.gz",
							Hash:        "abc123",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
						syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/uv-darwin-arm64.tar.gz",
							Hash:        "abc123arm",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
					syslist.OsTypeLinux: {
						syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/uv-linux-amd64.tar.gz",
							Hash:        "def456",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
				},
			},
		},
		"uv-legacy": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeDarwin: {
						syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/uv-old-darwin-amd64.tar.gz",
							Hash:        "old123",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
						syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/uv-old-darwin-arm64.tar.gz",
							Hash:        "old123arm",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
				},
			},
		},
		"system-uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{
				Command: "/usr/local/bin/uv",
			},
		},
		"fnm": {
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeManaged,
			FNM: &config.RuntimeConfigFNM{
				PNPMHash: "test-pnpm-sha256-hash",
			},
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeDarwin: {
						syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-darwin-amd64.tar.gz",
							Hash:        "fnm123",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
						syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-darwin-arm64.tar.gz",
							Hash:        "fnm123arm",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
					syslist.OsTypeLinux: {
						syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-linux-amd64.tar.gz",
							Hash:        "fnm456",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
				},
			},
		},
	}
}

func TestResolveRuntime(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("explicit runtime ref found", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("uv", config.RuntimeKindUV)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "uv" {
			t.Errorf("name = %q, want %q", name, "uv")
		}
		if rc.Kind != config.RuntimeKindUV {
			t.Errorf("kind = %q, want %q", rc.Kind, config.RuntimeKindUV)
		}
	})

	t.Run("explicit runtime ref for legacy version", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("uv-legacy", config.RuntimeKindUV)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "uv-legacy" {
			t.Errorf("name = %q, want %q", name, "uv-legacy")
		}
		if rc.Mode != config.RuntimeModeManaged {
			t.Errorf("mode = %q, want %q", rc.Mode, config.RuntimeModeManaged)
		}
	})

	t.Run("explicit runtime ref not found", func(t *testing.T) {
		_, _, err := rm.ResolveRuntime("nonexistent", config.RuntimeKindUV)
		if err == nil {
			t.Error("expected error for nonexistent runtime, got nil")
		}
	})

	t.Run("explicit runtime ref kind mismatch", func(t *testing.T) {
		_, _, err := rm.ResolveRuntime("uv", config.RuntimeKindFNM)
		if err == nil {
			t.Error("expected error for kind mismatch, got nil")
		}
	})

	t.Run("system runtime ref", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("system-uv", config.RuntimeKindUV)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "system-uv" {
			t.Errorf("name = %q, want %q", name, "system-uv")
		}
		if rc.Mode != config.RuntimeModeSystem {
			t.Errorf("mode = %q, want %q", rc.Mode, config.RuntimeModeSystem)
		}
	})

	t.Run("no runtime of kind returns error", func(t *testing.T) {
		rm2 := New(config.MapOfRuntimes{
			"uv": {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeManaged},
		})
		_, _, err := rm2.ResolveRuntime("", config.RuntimeKindFNM)
		if err == nil {
			t.Error("expected error when no runtime of kind exists, got nil")
		}
	})
}

func TestGetAppPath(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("managed runtime app path", func(t *testing.T) {
		path, err := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "uv")
		if err != nil {
			t.Fatalf("GetAppPath() error = %v", err)
		}
		if path == "" {
			t.Error("path is empty")
		}
	})

	t.Run("system runtime app path", func(t *testing.T) {
		path, err := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "system-uv")
		if err != nil {
			t.Fatalf("GetAppPath() error = %v", err)
		}
		if path == "" {
			t.Error("path is empty")
		}
	})

	t.Run("deterministic path", func(t *testing.T) {
		path1, _ := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "uv")
		path2, _ := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "uv")

		if path1 != path2 {
			t.Errorf("path not deterministic: %q != %q", path1, path2)
		}
	})

	t.Run("different versions produce different paths", func(t *testing.T) {
		path1, _ := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "uv")
		path2, _ := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.38.0", nil, "", nil, nil, "uv")

		if path1 == path2 {
			t.Error("different versions should produce different paths")
		}
	})

	t.Run("different runtimes produce different paths", func(t *testing.T) {
		path1, _ := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "uv")
		path2, _ := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "uv-legacy")

		if path1 == path2 {
			t.Error("different runtimes should produce different paths")
		}
	})

	t.Run("deps affect path", func(t *testing.T) {
		deps := map[string]string{"plugin": "1.0.0"}
		extra := FNMAppPathExtra{PackageName: "eslint", BinPath: "node_modules/.bin/eslint"}
		path1, _ := rm.GetAppPath("eslint", config.RuntimeKindFNM, "9.0.0", nil, "", nil, nil, "fnm", extra)
		path2, _ := rm.GetAppPath("eslint", config.RuntimeKindFNM, "9.0.0", deps, "", nil, nil, "fnm", extra)

		if path1 == path2 {
			t.Error("dependencies should affect path")
		}
	})

	t.Run("unknown runtime returns error", func(t *testing.T) {
		_, err := rm.GetAppPath("yamllint", config.RuntimeKindUV, "1.37.0", nil, "", nil, nil, "nonexistent")
		if err == nil {
			t.Error("expected error for unknown runtime, got nil")
		}
	})
}

func TestNewRuntimeManager(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	if rm == nil {
		t.Fatal("New() returned nil")
	}
	if rm.mapOfRuntimes == nil {
		t.Error("mapOfRuntimes is nil")
	}
	if len(rm.mapOfRuntimes) != len(runtimes) {
		t.Errorf("mapOfRuntimes length = %d, want %d", len(rm.mapOfRuntimes), len(runtimes))
	}
}

func TestCollectRequiredRuntimes(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
		},
		"fnm": {
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeManaged,
		},
		"system-uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeSystem,
		},
	}

	t.Run("includeAll returns all runtimes sorted", func(t *testing.T) {
		apps := binmanager.MapOfApps{}
		result := CollectRequiredRuntimes(apps, runtimes, true)
		if len(result) != 3 {
			t.Fatalf("expected 3 runtimes, got %d", len(result))
		}
		if result[0] != "fnm" || result[1] != "system-uv" || result[2] != "uv" {
			t.Errorf("expected sorted [fnm system-uv uv], got %v", result)
		}
	})

	t.Run("required uv app collects default uv runtime", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		// Should find either "uv" or "system-uv" (both are RuntimeKindUV)
		if result[0] != "uv" && result[0] != "system-uv" {
			t.Errorf("expected a uv runtime, got %q", result[0])
		}
	})

	t.Run("required fnm app collects default fnm runtime", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"eslint": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "eslint",
					Version:     "9.0.0",

					BinPath:     "node_modules/.bin/eslint",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "fnm" {
			t.Errorf("expected fnm, got %q", result[0])
		}
	})

	t.Run("explicit runtime ref is used", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
					Runtime:     "system-uv",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "system-uv" {
			t.Errorf("expected system-uv, got %q", result[0])
		}
	})

	t.Run("optional apps excluded when includeAll is false", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: false,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for optional app, got %d: %v", len(result), result)
		}
	})

	t.Run("binary apps do not contribute runtimes", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"golangci-lint": {
				Required: true,
				Binary: &binmanager.AppConfigBinary{
					Binaries: binmanager.MapOfBinaries{},
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for binary app, got %d: %v", len(result), result)
		}
	})

	t.Run("deduplication across multiple apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
					Runtime:     "uv",
				},
			},
			"ruff": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "ruff",
					Version:     "0.3.0",
					Runtime:     "uv",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 deduplicated runtime, got %d: %v", len(result), result)
		}
		if result[0] != "uv" {
			t.Errorf("expected uv, got %q", result[0])
		}
	})

	t.Run("mixed uv and fnm apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
					Runtime:     "uv",
				},
			},
			"eslint": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "eslint",
					Version:     "9.0.0",

					BinPath:     "node_modules/.bin/eslint",
					Runtime:     "fnm",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 runtimes, got %d: %v", len(result), result)
		}
		if result[0] != "fnm" || result[1] != "uv" {
			t.Errorf("expected sorted [fnm uv], got %v", result)
		}
	})

	t.Run("nonexistent runtime ref is ignored", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
					Runtime:     "nonexistent",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for nonexistent ref, got %d: %v", len(result), result)
		}
	})

	t.Run("empty apps returns empty", func(t *testing.T) {
		result := CollectRequiredRuntimes(binmanager.MapOfApps{}, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for empty apps, got %d: %v", len(result), result)
		}
	})

	t.Run("empty runtimes returns empty", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, config.MapOfRuntimes{}, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes when no runtimes defined, got %d: %v", len(result), result)
		}
	})
}

func TestInstallRuntimes(t *testing.T) {
	t.Run("empty names returns empty stats", func(t *testing.T) {
		rm := New(makeTestRuntimes())
		stats, err := rm.InstallRuntimes([]string{}, 3)
		if err != nil {
			t.Fatalf("InstallRuntimes() error = %v", err)
		}
		if len(stats.Downloaded) != 0 || len(stats.AlreadyCached) != 0 || len(stats.Skipped) != 0 || len(stats.Failed) != 0 {
			t.Error("expected all empty stats for empty input")
		}
	})

	t.Run("system runtimes reported as already cached", func(t *testing.T) {
		rm := New(makeTestRuntimes())
		stats, err := rm.InstallRuntimes([]string{"system-uv"}, 3)
		if err != nil {
			t.Fatalf("InstallRuntimes() error = %v", err)
		}
		if len(stats.AlreadyCached) != 1 {
			t.Errorf("expected 1 already cached, got %d", len(stats.AlreadyCached))
		}
		if stats.AlreadyCached[0] != "system-uv" {
			t.Errorf("expected system-uv, got %q", stats.AlreadyCached[0])
		}
	})

	t.Run("unknown runtime is skipped", func(t *testing.T) {
		rm := New(makeTestRuntimes())
		stats, err := rm.InstallRuntimes([]string{"nonexistent"}, 3)
		if err != nil {
			t.Fatalf("InstallRuntimes() error = %v", err)
		}
		if len(stats.Skipped) != 1 {
			t.Errorf("expected 1 skipped, got %d", len(stats.Skipped))
		}
		if stats.Skipped[0] != "nonexistent" {
			t.Errorf("expected nonexistent, got %q", stats.Skipped[0])
		}
	})

	t.Run("managed runtime without config is skipped", func(t *testing.T) {
		runtimes := config.MapOfRuntimes{
			"broken": {
				Kind:    config.RuntimeKindUV,
				Mode:    config.RuntimeModeManaged,
				Managed: nil,
			},
		}
		rm := New(runtimes)
		stats, err := rm.InstallRuntimes([]string{"broken"}, 3)
		if err != nil {
			t.Fatalf("InstallRuntimes() error = %v", err)
		}
		if len(stats.Skipped) != 1 {
			t.Errorf("expected 1 skipped, got %d", len(stats.Skipped))
		}
	})

	t.Run("mixed system and unknown runtimes", func(t *testing.T) {
		rm := New(makeTestRuntimes())
		stats, err := rm.InstallRuntimes([]string{"system-uv", "nonexistent"}, 3)
		if err != nil {
			t.Fatalf("InstallRuntimes() error = %v", err)
		}
		if len(stats.AlreadyCached) != 1 {
			t.Errorf("expected 1 already cached, got %d", len(stats.AlreadyCached))
		}
		if len(stats.Skipped) != 1 {
			t.Errorf("expected 1 skipped, got %d", len(stats.Skipped))
		}
	})
}

func TestGetCommandInfoFNM(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("fnm app is not a runtime-managed app without fnm config", func(t *testing.T) {
		app := binmanager.App{}
		_, err := rm.GetCommandInfo("test", app)
		if err == nil {
			t.Error("expected error for app with no config, got nil")
		}
	})

	t.Run("fnm app delegates to FNM methods", func(t *testing.T) {
		app := binmanager.App{
			Fnm: &binmanager.AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.4.2",

				BinPath:     "node_modules/.bin/mmdc",
				Runtime:     "fnm",
			},
		}

		// InstallFNMApp will fail because there's no actual FNM binary to download,
		// but we can verify the dispatch works by checking that it attempts FNM installation
		_, err := rm.GetCommandInfo("mmdc", app)
		// The error should be from InstallFNMApp, not from "not a runtime-managed app"
		if err == nil {
			t.Skip("unexpected success - FNM binary not available in test env")
		}
		if err.Error() == `app "mmdc" is not a runtime-managed app` {
			t.Error("FNM app should be recognized as runtime-managed")
		}
	})
}

func TestCollectRequiredRuntimesFNM(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
		},
		"fnm": {
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeManaged,
		},
	}

	t.Run("required fnm app collects default fnm runtime", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"mmdc": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "@mermaid-js/mermaid-cli",
					Version:     "11.4.2",

					BinPath:     "node_modules/.bin/mmdc",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "fnm" {
			t.Errorf("expected fnm, got %q", result[0])
		}
	})

	t.Run("fnm app with explicit runtime ref", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"mmdc": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "@mermaid-js/mermaid-cli",
					Version:     "11.4.2",

					BinPath:     "node_modules/.bin/mmdc",
					Runtime:     "fnm",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "fnm" {
			t.Errorf("expected fnm, got %q", result[0])
		}
	})

	t.Run("optional fnm app excluded", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"mmdc": {
				Required: false,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "@mermaid-js/mermaid-cli",
					Version:     "11.4.2",

					BinPath:     "node_modules/.bin/mmdc",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for optional fnm app, got %d: %v", len(result), result)
		}
	})

	t.Run("fnm app with nonexistent runtime ref ignored", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"mmdc": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "@mermaid-js/mermaid-cli",
					Version:     "11.4.2",

					BinPath:     "node_modules/.bin/mmdc",
					Runtime:     "nonexistent",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for nonexistent ref, got %d: %v", len(result), result)
		}
	})

	t.Run("mixed uv and fnm apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
					Runtime:     "uv",
				},
			},
			"mmdc": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "@mermaid-js/mermaid-cli",
					Version:     "11.4.2",

					BinPath:     "node_modules/.bin/mmdc",
					Runtime:     "fnm",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 runtimes, got %d: %v", len(result), result)
		}
		if result[0] != "fnm" || result[1] != "uv" {
			t.Errorf("expected sorted [fnm uv], got %v", result)
		}
	})

	t.Run("fnm deduplication across multiple apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"mmdc": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "@mermaid-js/mermaid-cli",
					Version:     "11.4.2",

					BinPath:     "node_modules/.bin/mmdc",
					Runtime:     "fnm",
				},
			},
			"slidev": {
				Required: true,
				Fnm: &binmanager.AppConfigFNM{
					PackageName: "@slidev/cli",
					Version:     "0.50.0",

					BinPath:     "node_modules/.bin/slidev",
					Runtime:     "fnm",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 deduplicated runtime, got %d: %v", len(result), result)
		}
		if result[0] != "fnm" {
			t.Errorf("expected fnm, got %q", result[0])
		}
	})

	t.Run("includeAll returns all runtimes including fnm", func(t *testing.T) {
		apps := binmanager.MapOfApps{}
		result := CollectRequiredRuntimes(apps, runtimes, true)
		if len(result) != 2 {
			t.Fatalf("expected 2 runtimes, got %d: %v", len(result), result)
		}
		if result[0] != "fnm" || result[1] != "uv" {
			t.Errorf("expected sorted [fnm uv], got %v", result)
		}
	})
}

func TestGetAppPathFNM(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("fnm app path with FNMAppPathExtra", func(t *testing.T) {
		path, err := rm.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm", FNMAppPathExtra{
			PackageName: "@mermaid-js/mermaid-cli",
			BinPath:     "node_modules/.bin/mmdc",
		})
		if err != nil {
			t.Fatalf("GetAppPath() error = %v", err)
		}
		if path == "" {
			t.Error("path is empty")
		}
	})

	t.Run("fnm app path is deterministic", func(t *testing.T) {
		extra := FNMAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		path1, _ := rm.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm", extra)
		path2, _ := rm.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm", extra)

		if path1 != path2 {
			t.Errorf("path not deterministic: %q != %q", path1, path2)
		}
	})

	t.Run("different node versions produce different paths", func(t *testing.T) {
		// Node version is now on the runtime config, so we need different runtimes
		runtimesWithDiffNode := makeTestRuntimes()
		runtimesWithDiffNode["fnm-alt-node"] = config.RuntimeConfig{
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeManaged,
			FNM: &config.RuntimeConfigFNM{
				NodeVersion: "20.11.1",
				PNPMVersion: "10.7.0",
				PNPMHash:    "test-pnpm-sha256-hash",
			},
			Managed: runtimesWithDiffNode["fnm"].Managed,
		}
		rmDiffNode := New(runtimesWithDiffNode)
		extra := FNMAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		path1, _ := rmDiffNode.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm", extra)
		path2, _ := rmDiffNode.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm-alt-node", extra)

		if path1 == path2 {
			t.Error("different node versions should produce different paths")
		}
	})

	t.Run("different pnpm versions produce different paths", func(t *testing.T) {
		runtimesWithDiffPNPM := makeTestRuntimes()
		runtimesWithDiffPNPM["fnm-alt-pnpm"] = config.RuntimeConfig{
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeManaged,
			FNM: &config.RuntimeConfigFNM{
				NodeVersion: "22.14.0",
				PNPMVersion: "9.15.0",
				PNPMHash:    "test-pnpm-sha256-hash",
			},
			Managed: runtimesWithDiffPNPM["fnm"].Managed,
		}
		rmDiffPNPM := New(runtimesWithDiffPNPM)
		extra := FNMAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		path1, _ := rmDiffPNPM.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm", extra)
		path2, _ := rmDiffPNPM.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm-alt-pnpm", extra)

		if path1 == path2 {
			t.Error("different pnpm versions should produce different paths")
		}
	})

	t.Run("fnm without FNMAppPathExtra uses standard hash", func(t *testing.T) {
		pathWithExtra, _ := rm.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm", FNMAppPathExtra{
			PackageName: "@mermaid-js/mermaid-cli",
			BinPath:     "node_modules/.bin/mmdc",
		})
		pathWithoutExtra, _ := rm.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm")

		if pathWithExtra == pathWithoutExtra {
			t.Error("FNM path with extra should differ from path without extra (different hash functions)")
		}
	})

	t.Run("fnm deps affect path", func(t *testing.T) {
		extra := FNMAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		deps := map[string]string{"puppeteer": "21.0.0"}
		path1, _ := rm.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", nil, "", nil, nil, "fnm", extra)
		path2, _ := rm.GetAppPath("mmdc", config.RuntimeKindFNM, "11.4.2", deps, "", nil, nil, "fnm", extra)

		if path1 == path2 {
			t.Error("dependencies should affect FNM app path")
		}
	})
}

func TestCollectRequiredRuntimesJVM(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
		},
		"jvm": {
			Kind: config.RuntimeKindJVM,
			Mode: config.RuntimeModeManaged,
			JVM:  &config.RuntimeConfigJVM{JavaVersion: "21"},
		},
	}

	t.Run("required jvm app collects default jvm runtime", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"openapi-generator": {
				Required: true,
				Jvm: &binmanager.AppConfigJVM{
					JarURL:  "https://example.com/openapi-generator.jar",
					JarHash: "abc123",
					Version: "7.0.0",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "jvm" {
			t.Errorf("expected jvm, got %q", result[0])
		}
	})

	t.Run("jvm app with explicit runtime ref", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"openapi-generator": {
				Required: true,
				Jvm: &binmanager.AppConfigJVM{
					JarURL:  "https://example.com/openapi-generator.jar",
					JarHash: "abc123",
					Version: "7.0.0",
					Runtime: "jvm",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "jvm" {
			t.Errorf("expected jvm, got %q", result[0])
		}
	})

	t.Run("optional jvm app excluded", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"openapi-generator": {
				Required: false,
				Jvm: &binmanager.AppConfigJVM{
					JarURL:  "https://example.com/openapi-generator.jar",
					JarHash: "abc123",
					Version: "7.0.0",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for optional jvm app, got %d: %v", len(result), result)
		}
	})

	t.Run("mixed uv and jvm apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
					Runtime:     "uv",
				},
			},
			"openapi-generator": {
				Required: true,
				Jvm: &binmanager.AppConfigJVM{
					JarURL:  "https://example.com/openapi-generator.jar",
					JarHash: "abc123",
					Version: "7.0.0",
					Runtime: "jvm",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 runtimes, got %d: %v", len(result), result)
		}
		if result[0] != "jvm" || result[1] != "uv" {
			t.Errorf("expected sorted [jvm uv], got %v", result)
		}
	})
}

func TestGetCommandInfoJVM(t *testing.T) {
	runtimes := makeTestRuntimes()
	runtimes["jvm"] = config.RuntimeConfig{
		Kind: config.RuntimeKindJVM,
		Mode: config.RuntimeModeManaged,
		JVM:  &config.RuntimeConfigJVM{JavaVersion: "21"},
		Managed: &config.RuntimeConfigManaged{
			Binaries: binmanager.MapOfBinaries{
				syslist.OsTypeDarwin: {
					syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
						URL:         "https://example.com/jdk-darwin-amd64.tar.gz",
						Hash:        "jdk123",
						ContentType: binmanager.BinContentTypeTarGz,
					}},
					syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
						URL:         "https://example.com/jdk-darwin-arm64.tar.gz",
						Hash:        "jdk123arm",
						ContentType: binmanager.BinContentTypeTarGz,
					}},
				},
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
						URL:         "https://example.com/jdk-linux-amd64.tar.gz",
						Hash:        "jdk456",
						ContentType: binmanager.BinContentTypeTarGz,
					}},
				},
			},
		},
	}
	rm := New(runtimes)

	t.Run("jvm app delegates to JVM methods", func(t *testing.T) {
		app := binmanager.App{
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "abc123",
				Version: "7.0.0",
				Runtime: "jvm",
			},
		}

		// InstallJVMApp will fail because there's no actual JDK binary to download,
		// but we can verify the dispatch works by checking that it attempts JVM installation
		_, err := rm.GetCommandInfo("openapi-generator", app)
		if err == nil {
			t.Skip("unexpected success - JDK binary not available in test env")
		}
		if err.Error() == `app "openapi-generator" is not a runtime-managed app` {
			t.Error("JVM app should be recognized as runtime-managed")
		}
	})
}

func TestComputeAppPathJVM(t *testing.T) {
	runtimes := makeTestRuntimes()
	runtimes["jvm"] = config.RuntimeConfig{
		Kind: config.RuntimeKindJVM,
		Mode: config.RuntimeModeManaged,
		JVM:  &config.RuntimeConfigJVM{JavaVersion: "21"},
		Managed: &config.RuntimeConfigManaged{
			Binaries: binmanager.MapOfBinaries{
				syslist.OsTypeDarwin: {
					syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
						URL:         "https://example.com/jdk-darwin-amd64.tar.gz",
						Hash:        "jdk123",
						ContentType: binmanager.BinContentTypeTarGz,
					}},
					syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
						URL:         "https://example.com/jdk-darwin-arm64.tar.gz",
						Hash:        "jdk123arm",
						ContentType: binmanager.BinContentTypeTarGz,
					}},
				},
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
						URL:         "https://example.com/jdk-linux-amd64.tar.gz",
						Hash:        "jdk456",
						ContentType: binmanager.BinContentTypeTarGz,
					}},
				},
			},
		},
	}
	rm := New(runtimes)

	t.Run("jvm app computes path", func(t *testing.T) {
		app := binmanager.App{
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "abc123",
				Version: "7.0.0",
				Runtime: "jvm",
			},
		}

		path, err := rm.ComputeAppPath("openapi-generator", app)
		if err != nil {
			t.Fatalf("ComputeAppPath() error = %v", err)
		}
		if path == "" {
			t.Error("path is empty")
		}
	})

	t.Run("jvm app path is deterministic", func(t *testing.T) {
		app := binmanager.App{
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "abc123",
				Version: "7.0.0",
				Runtime: "jvm",
			},
		}

		path1, _ := rm.ComputeAppPath("openapi-generator", app)
		path2, _ := rm.ComputeAppPath("openapi-generator", app)
		if path1 != path2 {
			t.Errorf("path not deterministic: %q != %q", path1, path2)
		}
	})

	t.Run("different versions produce different paths", func(t *testing.T) {
		app1 := binmanager.App{
			Jvm: &binmanager.AppConfigJVM{
				JarURL: "https://example.com/openapi-generator.jar", JarHash: "abc123", Version: "7.0.0", Runtime: "jvm",
			},
		}
		app2 := binmanager.App{
			Jvm: &binmanager.AppConfigJVM{
				JarURL: "https://example.com/openapi-generator.jar", JarHash: "abc123", Version: "7.1.0", Runtime: "jvm",
			},
		}

		path1, _ := rm.ComputeAppPath("openapi-generator", app1)
		path2, _ := rm.ComputeAppPath("openapi-generator", app2)
		if path1 == path2 {
			t.Error("different versions should produce different paths")
		}
	})
}

func TestResolveRuntimeJVM(t *testing.T) {
	runtimes := makeTestRuntimes()
	runtimes["jvm"] = config.RuntimeConfig{
		Kind: config.RuntimeKindJVM,
		Mode: config.RuntimeModeManaged,
		JVM:  &config.RuntimeConfigJVM{JavaVersion: "21"},
		Managed: &config.RuntimeConfigManaged{
			Binaries: binmanager.MapOfBinaries{},
		},
	}
	rm := New(runtimes)

	t.Run("explicit jvm runtime ref", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("jvm", config.RuntimeKindJVM)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "jvm" {
			t.Errorf("name = %q, want %q", name, "jvm")
		}
		if rc.Kind != config.RuntimeKindJVM {
			t.Errorf("kind = %q, want %q", rc.Kind, config.RuntimeKindJVM)
		}
	})

	t.Run("default fallback for jvm kind", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("", config.RuntimeKindJVM)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "jvm" {
			t.Errorf("name = %q, want %q", name, "jvm")
		}
		if rc.Kind != config.RuntimeKindJVM {
			t.Errorf("kind = %q, want %q", rc.Kind, config.RuntimeKindJVM)
		}
	})

	t.Run("jvm kind mismatch with uv runtime", func(t *testing.T) {
		_, _, err := rm.ResolveRuntime("uv", config.RuntimeKindJVM)
		if err == nil {
			t.Error("expected error for kind mismatch, got nil")
		}
	})
}

func TestResolveRuntimeFNM(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("explicit fnm runtime ref", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("fnm", config.RuntimeKindFNM)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "fnm" {
			t.Errorf("name = %q, want %q", name, "fnm")
		}
		if rc.Kind != config.RuntimeKindFNM {
			t.Errorf("kind = %q, want %q", rc.Kind, config.RuntimeKindFNM)
		}
	})

	t.Run("default fallback for fnm kind", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("", config.RuntimeKindFNM)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "fnm" {
			t.Errorf("name = %q, want %q", name, "fnm")
		}
		if rc.Kind != config.RuntimeKindFNM {
			t.Errorf("kind = %q, want %q", rc.Kind, config.RuntimeKindFNM)
		}
	})

	t.Run("fnm kind mismatch with uv runtime", func(t *testing.T) {
		_, _, err := rm.ResolveRuntime("uv", config.RuntimeKindFNM)
		if err == nil {
			t.Error("expected error for kind mismatch, got nil")
		}
	})
}

func newTestRMWithTarget(runtimes config.MapOfRuntimes, hostTarget target.Target) *RuntimeManager {
	return &RuntimeManager{
		mapOfRuntimes: runtimes,
		hostTarget:    hostTarget,
		lookPathFunc:  exec.LookPath,
	}
}

func newTestRMWithLookPath(runtimes config.MapOfRuntimes, hostTarget target.Target, lp func(string) (string, error)) *RuntimeManager {
	return &RuntimeManager{
		mapOfRuntimes: runtimes,
		hostTarget:    hostTarget,
		lookPathFunc:  lp,
	}
}

func glibcOnlyBinaries() binmanager.MapOfBinaries {
	return binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {"glibc": binmanager.BinaryOsArchInfo{
				URL:         "https://example.com/runtime-linux-amd64.tar.gz",
				Hash:        "abc123",
				ContentType: binmanager.BinContentTypeTarGz,
			}},
		},
	}
}

func TestResolveEffectiveRuntimeConfig_MuslFallbackToSystem(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "fnm" {
			return "/usr/bin/fnm", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("fnm", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q", result.Mode, config.RuntimeModeSystem)
	}
	if result.System == nil {
		t.Fatal("System config is nil")
	}
	if result.System.Command != "/usr/bin/fnm" {
		t.Errorf("System.Command = %q, want %q", result.System.Command, "/usr/bin/fnm")
	}
}

func TestResolveEffectiveRuntimeConfig_UVFallbackToSystem(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindUV,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"uv": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "uv" {
			return "/usr/bin/uv", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("uv", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q", result.Mode, config.RuntimeModeSystem)
	}
	if result.System == nil {
		t.Fatal("System config is nil")
	}
	if result.System.Command != "/usr/bin/uv" {
		t.Errorf("System.Command = %q, want %q", result.System.Command, "/usr/bin/uv")
	}
}

func TestResolveEffectiveRuntimeConfig_JVMFallbackToSystem(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindJVM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"jvm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "java" {
			return "/usr/bin/java", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("jvm", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q", result.Mode, config.RuntimeModeSystem)
	}
	if result.System == nil {
		t.Fatal("System config is nil")
	}
	if result.System.Command != "/usr/bin/java" {
		t.Errorf("System.Command = %q, want %q", result.System.Command, "/usr/bin/java")
	}
}

func TestResolveEffectiveRuntimeConfig_MuslNoSystemBinary(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("fnm", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (should remain managed when no system binary)", result.Mode, config.RuntimeModeManaged)
	}
	if result.System != nil {
		t.Error("System config should be nil when no fallback occurs")
	}
}

func TestResolveEffectiveRuntimeConfig_GlibcHost(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	// Mock lookPath to succeed -- glibc guard must prevent fallback even when system binary exists
	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcGlibc,
	}, func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	})

	result := rm.resolveEffectiveRuntimeConfig("fnm", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (glibc host should not trigger fallback)", result.Mode, config.RuntimeModeManaged)
	}
}

func TestResolveEffectiveRuntimeConfig_SystemMode(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeSystem,
		System: &config.RuntimeConfigSystem{
			Command: "/usr/local/bin/fnm",
		},
	}

	rm := newTestRMWithTarget(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	})

	result := rm.resolveEffectiveRuntimeConfig("fnm", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q (already system mode should not change)", result.Mode, config.RuntimeModeSystem)
	}
	if result.System.Command != "/usr/local/bin/fnm" {
		t.Errorf("System.Command = %q, want %q (should keep original command)", result.System.Command, "/usr/local/bin/fnm")
	}
}

func TestResolveEffectiveRuntimeConfig_MuslBinaryPresent(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: binmanager.MapOfBinaries{
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {
						"glibc": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-linux-amd64.tar.gz",
							Hash:        "abc123",
							ContentType: binmanager.BinContentTypeTarGz,
						},
						"musl": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-linux-amd64-musl.tar.gz",
							Hash:        "def456",
							ContentType: binmanager.BinContentTypeTarGz,
						},
					},
				},
			},
		},
	}

	// Mock lookPath to succeed -- musl binary exists so no fallback should occur
	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	})

	result := rm.resolveEffectiveRuntimeConfig("fnm", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (musl binary available, no fallback needed)", result.Mode, config.RuntimeModeManaged)
	}
}

func TestResolveEffectiveRuntimeConfig_ArchMismatch(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(), // only has amd64
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "arm64", // host is arm64 but binaries only have amd64
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	})

	result := rm.resolveEffectiveRuntimeConfig("fnm", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (arch mismatch, no fallback)", result.Mode, config.RuntimeModeManaged)
	}
}

func TestResolveEffectiveRuntimeConfig_PreservesSystemVersion(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
		System: &config.RuntimeConfigSystem{
			Command:       "/old/path/fnm",
			SystemVersion: "1.2.3",
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "fnm" {
			return "/usr/bin/fnm", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("fnm", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q", result.Mode, config.RuntimeModeSystem)
	}
	if result.System == nil {
		t.Fatal("System config is nil")
	}
	if result.System.Command != "/usr/bin/fnm" {
		t.Errorf("System.Command = %q, want %q", result.System.Command, "/usr/bin/fnm")
	}
	if result.System.SystemVersion != "1.2.3" {
		t.Errorf("System.SystemVersion = %q, want %q (should be preserved from original config)", result.System.SystemVersion, "1.2.3")
	}
}

func TestGetRuntimePath_MuslAutoFallback(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		FNM: &config.RuntimeConfigFNM{
			PNPMHash: "test-pnpm-sha256-hash",
		},
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "fnm" {
			return "/usr/bin/fnm", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	path, err := rm.GetRuntimePath("fnm")
	if err != nil {
		t.Fatalf("GetRuntimePath() error = %v", err)
	}
	if path != "/usr/bin/fnm" {
		t.Errorf("GetRuntimePath() = %q, want %q (should fallback to system fnm)", path, "/usr/bin/fnm")
	}
}

func TestSystemCommandForKind(t *testing.T) {
	tests := []struct {
		kind config.RuntimeKind
		want string
	}{
		{config.RuntimeKindFNM, "fnm"},
		{config.RuntimeKindUV, "uv"},
		{config.RuntimeKindJVM, "java"},
		{config.RuntimeKind("unknown"), ""},
		{config.RuntimeKind(""), ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			got := systemCommandForKind(tt.kind)
			if got != tt.want {
				t.Errorf("systemCommandForKind(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func TestInstallRuntimes_MuslAutoFallback(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindFNM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"fnm": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "fnm" {
			return "/usr/bin/fnm", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	stats, err := rm.InstallRuntimes([]string{"fnm"}, 3)
	if err != nil {
		t.Fatalf("InstallRuntimes() error = %v", err)
	}
	if len(stats.AlreadyCached) != 1 {
		t.Errorf("expected 1 already cached (system mode skip), got %d", len(stats.AlreadyCached))
	}
	if len(stats.AlreadyCached) > 0 && stats.AlreadyCached[0] != "fnm" {
		t.Errorf("expected fnm in already cached, got %q", stats.AlreadyCached[0])
	}
	if len(stats.Downloaded) != 0 {
		t.Errorf("expected 0 downloads (system fallback should skip), got %d", len(stats.Downloaded))
	}
}
