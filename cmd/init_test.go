package cmd

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckInitGitRoot(t *testing.T) {
	t.Run("same path returns nil", func(t *testing.T) {
		err := checkInitGitRoot("/home/user/project", "/home/user/project")
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("cleaned paths match", func(t *testing.T) {
		err := checkInitGitRoot("/home/user/project/", "/home/user/project")
		if err != nil {
			t.Errorf("expected nil for trailing slash difference, got %v", err)
		}
	})

	t.Run("subdirectory returns error", func(t *testing.T) {
		err := checkInitGitRoot("/home/user/project/src", "/home/user/project")
		if err == nil {
			t.Fatal("expected error when cwd is a subdirectory")
		}
		if !strings.Contains(err.Error(), "init must be run from git root") {
			t.Errorf("error should mention git root requirement, got: %v", err)
		}
		if !strings.Contains(err.Error(), "/home/user/project/src") {
			t.Errorf("error should contain cwd path, got: %v", err)
		}
		if !strings.Contains(err.Error(), "/home/user/project") {
			t.Errorf("error should contain git root path, got: %v", err)
		}
	})

	t.Run("completely different paths return error", func(t *testing.T) {
		err := checkInitGitRoot("/tmp/other", "/home/user/project")
		if err == nil {
			t.Fatal("expected error when cwd differs from git root")
		}
	})
}

func TestHasAnyLinks(t *testing.T) {
	t.Run("no apps no bundles", func(t *testing.T) {
		if hasAnyLinks(binmanager.MapOfApps{}, nil) {
			t.Error("expected false for empty apps and nil bundles")
		}
	})

	t.Run("apps without links", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"eslint": {Files: map[string]string{"a.js": "content"}},
			"prettier": {},
		}
		if hasAnyLinks(apps, nil) {
			t.Error("expected false when no apps have links")
		}
	})

	t.Run("app with links", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"eslint": {
				Files: map[string]string{"a.js": "content"},
				Links: map[string]string{"a.js": "a.js"},
			},
		}
		if !hasAnyLinks(apps, nil) {
			t.Error("expected true when an app has links")
		}
	})

	t.Run("bundle with links", func(t *testing.T) {
		bundles := binmanager.MapOfBundles{
			"my-bundle": {
				Version: "1.0",
				Files:   map[string]string{"a.txt": "content"},
				Links:   map[string]string{"a": "a.txt"},
			},
		}
		if !hasAnyLinks(binmanager.MapOfApps{}, bundles) {
			t.Error("expected true when a bundle has links")
		}
	})

	t.Run("bundle without links", func(t *testing.T) {
		bundles := binmanager.MapOfBundles{
			"my-bundle": {
				Version: "1.0",
				Files:   map[string]string{"a.txt": "content"},
			},
		}
		if hasAnyLinks(binmanager.MapOfApps{}, bundles) {
			t.Error("expected false when bundle has no links")
		}
	})
}

type mockInstallRootResolver struct {
	paths map[string]string
}

func (m *mockInstallRootResolver) GetInstallRoot(appName string) (string, error) {
	p, ok := m.paths[appName]
	if !ok {
		return "", fmt.Errorf("app %q is not installed", appName)
	}
	return p, nil
}

func TestSetupConfigLinks_CreatesSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "cache", "eslint", "abc123")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "eslint-base.js"), []byte("module.exports = {};"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Binary: &binmanager.AppConfigBinary{},
			Files:  map[string]string{"eslint-base.js": "module.exports = {};"},
			Links:  map[string]string{"eslint-base.js": "eslint-base.js"},
		},
	}

	if !hasAnyLinks(apps, nil) {
		t.Fatal("hasAnyLinks should return true")
	}

	resolver := &mockInstallRootResolver{
		paths: map[string]string{"eslint": installDir},
	}

	if _, err := managedconfig.CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	linkPath := filepath.Join(gitRoot, ".datamitsu", "eslint-base.js")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}

	expectedTarget, err := filepath.Rel(filepath.Join(gitRoot, ".datamitsu"), filepath.Join(installDir, "eslint-base.js"))
	if err != nil {
		t.Fatalf("failed to compute expected relative target: %v", err)
	}
	if target != expectedTarget {
		t.Errorf("symlink target = %q, want %q", target, expectedTarget)
	}

	content, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("failed to read through symlink: %v", err)
	}
	if string(content) != "module.exports = {};" {
		t.Errorf("content = %q, want %q", string(content), "module.exports = {};")
	}
}

func TestSetupConfigLinks_NoLinksSkipped(t *testing.T) {
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"eslint": {Binary: &binmanager.AppConfigBinary{}},
		},
	}

	// setupConfigLinks should return nil immediately when no apps have links
	err := setupConfigLinks(t.TempDir(), cfg, nil, false)
	if err != nil {
		t.Fatalf("expected nil error for apps without links, got: %v", err)
	}
}

func TestInitCommandRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "init" {
			found = true
			break
		}
	}
	if !found {
		t.Error("init command not registered with rootCmd")
	}
}

func TestSetupConfigLinks_BundleLinksAppearInDatamitsu(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	bundleDir := filepath.Join(tmpDir, "bundles", "skills", "hash123")

	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "agents.md"), []byte("# Agents"), 0644); err != nil {
		t.Fatal(err)
	}

	bundles := binmanager.MapOfBundles{
		"skills": {
			Version: "1.0",
			Files:   map[string]string{"agents.md": "# Agents"},
			Links:   map[string]string{"agents": "agents.md"},
		},
	}

	bundleResolver := &mockInstallRootResolver{
		paths: map[string]string{"skills": bundleDir},
	}

	links, err := managedconfig.CreateDatamitsuLinks(gitRoot, binmanager.MapOfApps{}, nil, bundles, bundleResolver, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	if len(links) != 1 || links[0] != "agents" {
		t.Errorf("expected [agents], got %v", links)
	}

	linkPath := filepath.Join(gitRoot, ".datamitsu", "agents")
	content, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("failed to read through symlink: %v", err)
	}
	if string(content) != "# Agents" {
		t.Errorf("content = %q, want %q", string(content), "# Agents")
	}
}

func TestInstallBundles_InInitFlow(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := binmanager.MapOfBundles{
		"test-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"readme.md": "# Hello",
			},
			Links: map[string]string{
				"readme": "readme.md",
			},
		},
	}

	bm := binmanager.New(binmanager.MapOfApps{}, bundles, nil)
	ctx := context.Background()

	err := installBundles(ctx, bm, false)
	if err != nil {
		t.Fatalf("installBundles failed: %v", err)
	}

	root, err := bm.GetBundleRoot("test-bundle")
	if err != nil {
		t.Fatalf("GetBundleRoot failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "readme.md"))
	if err != nil {
		t.Fatalf("failed to read installed file: %v", err)
	}
	if string(content) != "# Hello" {
		t.Errorf("content = %q, want %q", string(content), "# Hello")
	}
}

func TestInstallBundles_SkipDownloadSkipsExternal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := binmanager.MapOfBundles{
		"inline-bundle": {
			Version: "1.0",
			Files:   map[string]string{"a.txt": "inline"},
		},
		"external-bundle": {
			Version: "1.0",
			Files:   map[string]string{"b.txt": "has external"},
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {URL: "https://example.com/dist.tar.gz", Hash: "abc", Format: "tar.gz"},
			},
		},
	}

	bm := binmanager.New(binmanager.MapOfApps{}, bundles, nil)
	ctx := context.Background()

	err := installBundles(ctx, bm, true)
	if err != nil {
		t.Fatalf("installBundles failed: %v", err)
	}

	// Inline bundle should be installed
	_, err = bm.GetBundleRoot("inline-bundle")
	if err != nil {
		t.Errorf("inline bundle should be installed: %v", err)
	}

	// External bundle should NOT be installed
	_, err = bm.GetBundleRoot("external-bundle")
	if err == nil {
		t.Error("external bundle should NOT be installed when skipDownload is true")
	}
}

func TestInitCreatesConfigTypeDefinitions(t *testing.T) {
	t.Run("type definitions file exists after CreateDatamitsuLinks", func(t *testing.T) {
		tmpDir := t.TempDir()
		gitRoot := filepath.Join(tmpDir, "repo")
		installDir := filepath.Join(tmpDir, "cache", "myapp", "abc123")

		if err := os.MkdirAll(installDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(installDir, "config.js"), []byte("// config"), 0644); err != nil {
			t.Fatal(err)
		}

		apps := binmanager.MapOfApps{
			"myapp": {
				Binary: &binmanager.AppConfigBinary{},
				Files:  map[string]string{"config.js": "// config"},
				Links:  map[string]string{"config.js": "config.js"},
			},
		}

		resolver := &mockInstallRootResolver{
			paths: map[string]string{"myapp": installDir},
		}

		if _, err := managedconfig.CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
			t.Fatalf("CreateDatamitsuLinks failed: %v", err)
		}

		dtsPath := filepath.Join(gitRoot, ".datamitsu", "datamitsu.config.d.ts")
		content, err := os.ReadFile(dtsPath)
		if err != nil {
			t.Fatalf("datamitsu.config.d.ts not created: %v", err)
		}

		expected := config.GetDefaultConfigDTS()
		if string(content) != expected {
			t.Errorf("type definitions content mismatch: got %d bytes, want %d bytes", len(content), len(expected))
		}
	})

	t.Run("type definitions file is overwritten on subsequent init", func(t *testing.T) {
		tmpDir := t.TempDir()
		gitRoot := filepath.Join(tmpDir, "repo")
		installDir := filepath.Join(tmpDir, "cache", "myapp", "abc123")

		if err := os.MkdirAll(installDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(installDir, "config.js"), []byte("// config"), 0644); err != nil {
			t.Fatal(err)
		}

		apps := binmanager.MapOfApps{
			"myapp": {
				Binary: &binmanager.AppConfigBinary{},
				Files:  map[string]string{"config.js": "// config"},
				Links:  map[string]string{"config.js": "config.js"},
			},
		}

		resolver := &mockInstallRootResolver{
			paths: map[string]string{"myapp": installDir},
		}

		// First init
		if _, err := managedconfig.CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
			t.Fatalf("first CreateDatamitsuLinks failed: %v", err)
		}

		// Tamper with the file to simulate stale content
		dtsPath := filepath.Join(gitRoot, ".datamitsu", "datamitsu.config.d.ts")
		if err := os.WriteFile(dtsPath, []byte("// stale content"), 0644); err != nil {
			t.Fatalf("failed to write stale content: %v", err)
		}

		// Second init - should overwrite
		if _, err := managedconfig.CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
			t.Fatalf("second CreateDatamitsuLinks failed: %v", err)
		}

		content, err := os.ReadFile(dtsPath)
		if err != nil {
			t.Fatalf("datamitsu.config.d.ts not found after second init: %v", err)
		}

		expected := config.GetDefaultConfigDTS()
		if string(content) != expected {
			t.Errorf("type definitions not overwritten: got %d bytes, want %d bytes", len(content), len(expected))
		}
		if string(content) == "// stale content" {
			t.Error("type definitions still contain stale content after second init")
		}
	})
}

func TestBundleRootResolver(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := binmanager.MapOfBundles{
		"test-bundle": {
			Version: "1.0",
			Files:   map[string]string{"a.txt": "hello"},
		},
	}

	bm := binmanager.New(binmanager.MapOfApps{}, bundles, nil)
	ctx := context.Background()

	_, err := bm.InstallBundles(ctx, false)
	if err != nil {
		t.Fatalf("InstallBundles failed: %v", err)
	}

	resolver := &bundleRootResolver{bm: bm}
	root, err := resolver.GetInstallRoot("test-bundle")
	if err != nil {
		t.Fatalf("GetInstallRoot failed: %v", err)
	}

	expectedRoot, _ := bm.GetBundleRoot("test-bundle")
	if root != expectedRoot {
		t.Errorf("resolver returned %q, want %q", root, expectedRoot)
	}
}
