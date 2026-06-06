package runtimemanager

import (
	"os/exec"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
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
		"node": {
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{
				PNPMHash: "test-pnpm-sha256-hash",
			},
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeDarwin: {
						syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/node-darwin-amd64.tar.gz",
							Hash:        "node123",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
						syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/node-darwin-arm64.tar.gz",
							Hash:        "node123arm",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
					syslist.OsTypeLinux: {
						syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/node-linux-amd64.tar.gz",
							Hash:        "node456",
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
		_, _, err := rm.ResolveRuntime("uv", config.RuntimeKindNode)
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
		_, _, err := rm2.ResolveRuntime("", config.RuntimeKindNode)
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
		extra := NodeAppPathExtra{PackageName: "eslint", BinPath: "node_modules/.bin/eslint"}
		path1, _ := rm.GetAppPath("eslint", config.RuntimeKindNode, "9.0.0", nil, "", nil, nil, "node", extra)
		path2, _ := rm.GetAppPath("eslint", config.RuntimeKindNode, "9.0.0", deps, "", nil, nil, "node", extra)

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
		"node": {
			Kind: config.RuntimeKindNode,
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
		if result[0] != "node" || result[1] != "system-uv" || result[2] != "uv" {
			t.Errorf("expected sorted [node system-uv uv], got %v", result)
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

	t.Run("required node app collects default node runtime", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"eslint": {
				Required: true,
				Node: &binmanager.AppConfigNode{
					PackageName: "eslint",
					Version:     "9.0.0",

					BinPath: "node_modules/.bin/eslint",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "node" {
			t.Errorf("expected node, got %q", result[0])
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

	t.Run("mixed uv and node apps", func(t *testing.T) {
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
				Node: &binmanager.AppConfigNode{
					PackageName: "eslint",
					Version:     "9.0.0",

					BinPath: "node_modules/.bin/eslint",
					Runtime: "node",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 runtimes, got %d: %v", len(result), result)
		}
		if result[0] != "node" || result[1] != "uv" {
			t.Errorf("expected sorted [node uv], got %v", result)
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

func TestGetAppPathNode(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("node app path with NodeAppPathExtra", func(t *testing.T) {
		path, err := rm.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node", NodeAppPathExtra{
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

	t.Run("node app path is deterministic", func(t *testing.T) {
		extra := NodeAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		path1, _ := rm.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node", extra)
		path2, _ := rm.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node", extra)

		if path1 != path2 {
			t.Errorf("path not deterministic: %q != %q", path1, path2)
		}
	})

	t.Run("different node versions produce different paths", func(t *testing.T) {
		// Node version is now on the runtime config, so we need different runtimes
		runtimesWithDiffNode := makeTestRuntimes()
		runtimesWithDiffNode["node-alt"] = config.RuntimeConfig{
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{
				NodeVersion: "20.11.1",
				PNPMVersion: "10.7.0",
				PNPMHash:    "test-pnpm-sha256-hash",
			},
			Managed: runtimesWithDiffNode["node"].Managed,
		}
		rmDiffNode := New(runtimesWithDiffNode)
		extra := NodeAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		path1, _ := rmDiffNode.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node", extra)
		path2, _ := rmDiffNode.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node-alt", extra)

		if path1 == path2 {
			t.Error("different node versions should produce different paths")
		}
	})

	t.Run("different pnpm versions produce different paths", func(t *testing.T) {
		runtimesWithDiffPNPM := makeTestRuntimes()
		runtimesWithDiffPNPM["node-alt-pnpm"] = config.RuntimeConfig{
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "9.15.0",
				PNPMHash:    "test-pnpm-sha256-hash",
			},
			Managed: runtimesWithDiffPNPM["node"].Managed,
		}
		rmDiffPNPM := New(runtimesWithDiffPNPM)
		extra := NodeAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		path1, _ := rmDiffPNPM.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node", extra)
		path2, _ := rmDiffPNPM.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node-alt-pnpm", extra)

		if path1 == path2 {
			t.Error("different pnpm versions should produce different paths")
		}
	})

	t.Run("node without NodeAppPathExtra uses standard hash", func(t *testing.T) {
		pathWithExtra, _ := rm.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node", NodeAppPathExtra{
			PackageName: "@mermaid-js/mermaid-cli",
			BinPath:     "node_modules/.bin/mmdc",
		})
		pathWithoutExtra, _ := rm.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node")

		if pathWithExtra == pathWithoutExtra {
			t.Error("node path with extra should differ from path without extra (different hash functions)")
		}
	})

	t.Run("node deps affect path", func(t *testing.T) {
		extra := NodeAppPathExtra{PackageName: "@mermaid-js/mermaid-cli", BinPath: "node_modules/.bin/mmdc"}
		deps := map[string]string{"puppeteer": "21.0.0"}
		path1, _ := rm.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", nil, "", nil, nil, "node", extra)
		path2, _ := rm.GetAppPath("mmdc", config.RuntimeKindNode, "11.4.2", deps, "", nil, nil, "node", extra)

		if path1 == path2 {
			t.Error("dependencies should affect node app path")
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

func TestResolveRuntimeNode(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("explicit node runtime ref", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("node", config.RuntimeKindNode)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "node" {
			t.Errorf("name = %q, want %q", name, "node")
		}
		if rc.Kind != config.RuntimeKindNode {
			t.Errorf("kind = %q, want %q", rc.Kind, config.RuntimeKindNode)
		}
	})

	t.Run("default fallback for node kind", func(t *testing.T) {
		name, rc, err := rm.ResolveRuntime("", config.RuntimeKindNode)
		if err != nil {
			t.Fatalf("ResolveRuntime() error = %v", err)
		}
		if name != "node" {
			t.Errorf("name = %q, want %q", name, "node")
		}
		if rc.Kind != config.RuntimeKindNode {
			t.Errorf("kind = %q, want %q", rc.Kind, config.RuntimeKindNode)
		}
	})

	t.Run("node kind mismatch with uv runtime", func(t *testing.T) {
		_, _, err := rm.ResolveRuntime("uv", config.RuntimeKindNode)
		if err == nil {
			t.Error("expected error for kind mismatch, got nil")
		}
	})
}

func TestCollectRequiredRuntimesGo(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
		},
		"go": {
			Kind: config.RuntimeKindGo,
			Mode: config.RuntimeModeManaged,
			Go:   &config.RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}

	t.Run("required go app collects default go runtime", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"govulncheck": {
				Required: true,
				Go: &binmanager.AppConfigGo{
					PackageName: "golang.org/x/vuln/cmd/govulncheck",
					Version:     "v1.1.4",
					LockFile:    "x",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "go" {
			t.Errorf("expected go, got %q", result[0])
		}
	})

	t.Run("go app with explicit runtime ref", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"govulncheck": {
				Required: true,
				Go: &binmanager.AppConfigGo{
					PackageName: "golang.org/x/vuln/cmd/govulncheck",
					Version:     "v1.1.4",
					Runtime:     "go",
					LockFile:    "x",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 runtime, got %d: %v", len(result), result)
		}
		if result[0] != "go" {
			t.Errorf("expected go, got %q", result[0])
		}
	})

	t.Run("optional go app excluded", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"govulncheck": {
				Required: false,
				Go: &binmanager.AppConfigGo{
					PackageName: "golang.org/x/vuln/cmd/govulncheck",
					Version:     "v1.1.4",
					LockFile:    "x",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for optional go app, got %d: %v", len(result), result)
		}
	})

	t.Run("go app with nonexistent runtime ref ignored", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"govulncheck": {
				Required: true,
				Go: &binmanager.AppConfigGo{
					PackageName: "golang.org/x/vuln/cmd/govulncheck",
					Version:     "v1.1.4",
					Runtime:     "nonexistent",
					LockFile:    "x",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for nonexistent ref, got %d: %v", len(result), result)
		}
	})

	t.Run("mixed uv and go apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "yamllint",
					Version:     "1.37.0",
					Runtime:     "uv",
				},
			},
			"govulncheck": {
				Required: true,
				Go: &binmanager.AppConfigGo{
					PackageName: "golang.org/x/vuln/cmd/govulncheck",
					Version:     "v1.1.4",
					Runtime:     "go",
					LockFile:    "x",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 runtimes, got %d: %v", len(result), result)
		}
		if result[0] != "go" || result[1] != "uv" {
			t.Errorf("expected sorted [go uv], got %v", result)
		}
	})

	t.Run("go deduplication across multiple apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"govulncheck": {
				Required: true,
				Go: &binmanager.AppConfigGo{
					PackageName: "golang.org/x/vuln/cmd/govulncheck",
					Version:     "v1.1.4",
					Runtime:     "go",
					LockFile:    "x",
				},
			},
			"staticcheck": {
				Required: true,
				Go: &binmanager.AppConfigGo{
					PackageName: "honnef.co/go/tools/cmd/staticcheck",
					Version:     "2024.1.1",
					Runtime:     "go",
					LockFile:    "x",
				},
			},
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 deduplicated runtime, got %d: %v", len(result), result)
		}
		if result[0] != "go" {
			t.Errorf("expected go, got %q", result[0])
		}
	})
}

func TestComputeAppPathGo(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	t.Run("go app computes path", func(t *testing.T) {
		app := binmanager.App{
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "go",
				LockFile:    "x",
			},
		}

		path, err := rm.ComputeAppPath("govulncheck", app)
		if err != nil {
			t.Fatalf("ComputeAppPath() error = %v", err)
		}
		if path == "" {
			t.Error("path is empty")
		}
	})

	t.Run("go app path is deterministic", func(t *testing.T) {
		app := binmanager.App{
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "go",
				LockFile:    "x",
			},
		}

		path1, _ := rm.ComputeAppPath("govulncheck", app)
		path2, _ := rm.ComputeAppPath("govulncheck", app)
		if path1 != path2 {
			t.Errorf("path not deterministic: %q != %q", path1, path2)
		}
	})

	t.Run("different versions produce different paths", func(t *testing.T) {
		app1 := binmanager.App{
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.4", Runtime: "go", LockFile: "x",
			},
		}
		app2 := binmanager.App{
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.5", Runtime: "go", LockFile: "x",
			},
		}

		path1, _ := rm.ComputeAppPath("govulncheck", app1)
		path2, _ := rm.ComputeAppPath("govulncheck", app2)
		if path1 == path2 {
			t.Error("different versions should produce different paths")
		}
	})

	t.Run("nonexistent runtime returns error", func(t *testing.T) {
		app := binmanager.App{
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "nonexistent",
				LockFile:    "x",
			},
		}
		if _, err := rm.ComputeAppPath("govulncheck", app); err == nil {
			t.Error("expected error for nonexistent runtime, got nil")
		}
	})
}

func TestGetCommandInfoGo(t *testing.T) {
	rm := New(makeTestGoRuntimes())

	t.Run("go app delegates to Go methods", func(t *testing.T) {
		app := binmanager.App{
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "nonexistent", // forces a deterministic error from the Go install path
				LockFile:    "x",
			},
		}

		// The dispatch must route a Go app to InstallGoApp/GetGoCommandInfo: the
		// error must originate there (failing to resolve "nonexistent"), not from
		// the fall-through "not a runtime-managed app". Using an unresolvable
		// runtime keeps this deterministic regardless of the host environment.
		_, err := rm.GetCommandInfo("govulncheck", app)
		if err == nil {
			t.Fatal("expected error resolving nonexistent Go runtime, got nil")
		}
		if err.Error() == `app "govulncheck" is not a runtime-managed app` {
			t.Error("Go app should be recognized as runtime-managed")
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
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "node" {
			return "/usr/bin/node", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("node", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q", result.Mode, config.RuntimeModeSystem)
	}
	if result.System == nil {
		t.Fatal("System config is nil")
	}
	if result.System.Command != "/usr/bin/node" {
		t.Errorf("System.Command = %q, want %q", result.System.Command, "/usr/bin/node")
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
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("node", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (should remain managed when no system binary)", result.Mode, config.RuntimeModeManaged)
	}
	if result.System != nil {
		t.Error("System config should be nil when no fallback occurs")
	}
}

func TestResolveEffectiveRuntimeConfig_GlibcHost(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	// Mock lookPath to succeed -- glibc guard must prevent fallback even when system binary exists
	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcGlibc,
	}, func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	})

	result := rm.resolveEffectiveRuntimeConfig("node", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (glibc host should not trigger fallback)", result.Mode, config.RuntimeModeManaged)
	}
}

func TestResolveEffectiveRuntimeConfig_SystemMode(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeSystem,
		System: &config.RuntimeConfigSystem{
			Command: "/usr/local/bin/node",
		},
	}

	rm := newTestRMWithTarget(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	})

	result := rm.resolveEffectiveRuntimeConfig("node", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q (already system mode should not change)", result.Mode, config.RuntimeModeSystem)
	}
	if result.System.Command != "/usr/local/bin/node" {
		t.Errorf("System.Command = %q, want %q (should keep original command)", result.System.Command, "/usr/local/bin/node")
	}
}

func TestResolveEffectiveRuntimeConfig_MuslBinaryPresent(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: binmanager.MapOfBinaries{
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {
						"glibc": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/node-linux-amd64.tar.gz",
							Hash:        "abc123",
							ContentType: binmanager.BinContentTypeTarGz,
						},
						"musl": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/node-linux-amd64-musl.tar.gz",
							Hash:        "def456",
							ContentType: binmanager.BinContentTypeTarGz,
						},
					},
				},
			},
		},
	}

	// Mock lookPath to succeed -- musl binary exists so no fallback should occur
	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	})

	result := rm.resolveEffectiveRuntimeConfig("node", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (musl binary available, no fallback needed)", result.Mode, config.RuntimeModeManaged)
	}
}

func TestResolveEffectiveRuntimeConfig_ArchMismatch(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(), // only has amd64
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "arm64", // host is arm64 but binaries only have amd64
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	})

	result := rm.resolveEffectiveRuntimeConfig("node", rc)
	if result.Mode != config.RuntimeModeManaged {
		t.Errorf("Mode = %q, want %q (arch mismatch, no fallback)", result.Mode, config.RuntimeModeManaged)
	}
}

func TestResolveEffectiveRuntimeConfig_PreservesSystemVersion(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
		System: &config.RuntimeConfigSystem{
			Command:       "/old/path/node",
			SystemVersion: "1.2.3",
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "node" {
			return "/usr/bin/node", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	result := rm.resolveEffectiveRuntimeConfig("node", rc)
	if result.Mode != config.RuntimeModeSystem {
		t.Errorf("Mode = %q, want %q", result.Mode, config.RuntimeModeSystem)
	}
	if result.System == nil {
		t.Fatal("System config is nil")
	}
	if result.System.Command != "/usr/bin/node" {
		t.Errorf("System.Command = %q, want %q", result.System.Command, "/usr/bin/node")
	}
	if result.System.SystemVersion != "1.2.3" {
		t.Errorf("System.SystemVersion = %q, want %q (should be preserved from original config)", result.System.SystemVersion, "1.2.3")
	}
}

func TestGetRuntimePath_MuslAutoFallback(t *testing.T) {
	rc := config.RuntimeConfig{
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Node: &config.RuntimeConfigNode{
			PNPMHash: "test-pnpm-sha256-hash",
		},
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "node" {
			return "/usr/bin/node", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	path, err := rm.GetRuntimePath("node")
	if err != nil {
		t.Fatalf("GetRuntimePath() error = %v", err)
	}
	if path != "/usr/bin/node" {
		t.Errorf("GetRuntimePath() = %q, want %q (should fallback to system node)", path, "/usr/bin/node")
	}
}

func TestSystemCommandForKind(t *testing.T) {
	tests := []struct {
		kind config.RuntimeKind
		want string
	}{
		{config.RuntimeKindUV, "uv"},
		{config.RuntimeKindJVM, "java"},
		{config.RuntimeKindGo, "go"},
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
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{
			Binaries: glibcOnlyBinaries(),
		},
	}

	rm := newTestRMWithLookPath(config.MapOfRuntimes{"node": rc}, target.Target{
		OS:   "linux",
		Arch: "amd64",
		Libc: target.LibcMusl,
	}, func(file string) (string, error) {
		if file == "node" {
			return "/usr/bin/node", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	})

	stats, err := rm.InstallRuntimes([]string{"node"}, 3)
	if err != nil {
		t.Fatalf("InstallRuntimes() error = %v", err)
	}
	if len(stats.AlreadyCached) != 1 {
		t.Errorf("expected 1 already cached (system mode skip), got %d", len(stats.AlreadyCached))
	}
	if len(stats.AlreadyCached) > 0 && stats.AlreadyCached[0] != "node" {
		t.Errorf("expected node in already cached, got %q", stats.AlreadyCached[0])
	}
	if len(stats.Downloaded) != 0 {
		t.Errorf("expected 0 downloads (system fallback should skip), got %d", len(stats.Downloaded))
	}
}

// TestValidateRelativePath pins the path-escape contract for validateRelativePath
// and keeps it aligned with config.validateSafeRelativePath: only ".." or a path
// whose first cleaned segment is ".." (e.g. "../escape") escapes. A directory name
// that merely starts with the literal ".." (e.g. "..config/bin/tool") is a valid
// relative path and must be accepted.
func TestValidateRelativePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		// Accepted: a plain relative path under the runtime cache.
		{name: "simple relative", path: "bin/tool", wantErr: false},
		// Accepted: leading ".." is part of a directory name, not a parent ref.
		{name: "dotdot prefix in name", path: "..config/bin/tool", wantErr: false},
		{name: "dotdot prefix single segment", path: "..hidden", wantErr: false},
		// Rejected: bare parent reference.
		{name: "bare dotdot", path: "..", wantErr: true},
		// Rejected: leading parent escape.
		{name: "leading escape", path: "../escape", wantErr: true},
		// Rejected: embedded parent escape that cleans to an escape.
		{name: "embedded escape", path: "bin/../../escape", wantErr: true},
		// Rejected: absolute path.
		{name: "absolute path", path: "/etc/passwd", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRelativePath(tt.path)
			if tt.wantErr && err == nil {
				t.Errorf("validateRelativePath(%q) = nil, want error", tt.path)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateRelativePath(%q) = %v, want nil", tt.path, err)
			}
		})
	}
}
