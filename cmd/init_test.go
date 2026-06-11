package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/ui"
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
			"eslint":   {Files: map[string]string{"a.js": "content"}},
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

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "eslint-base.js"), []byte("module.exports = {};"), 0o644); err != nil {
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

	// reportConfigLinks should return nil immediately when no apps have links
	_, err := reportConfigLinks(ui.New(term.Plain), t.TempDir(), cfg, nil, false)
	if err != nil {
		t.Fatalf("expected nil error for apps without links, got: %v", err)
	}
}

// TestMaterializeInstalledLinks_SkipsDeferredNoError is the regression test for
// the PR: a link-app that declares Links but is NOT installed (deferred/Lazy)
// must be silently skipped by the link pass — never a "source not installed"
// hard error — while installed link-apps are still linked.
func TestMaterializeInstalledLinks_SkipsDeferredNoError(t *testing.T) {
	tmp := t.TempDir()
	gitRoot := filepath.Join(tmp, "repo")

	installedDir := filepath.Join(tmp, "store", "installed", "h1")
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installedDir, "config.js"), []byte("export default {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"installed-link": {
				Node:  &binmanager.AppConfigNode{PackageName: "p1"},
				Links: map[string]string{"installed.config.js": "config.js"},
			},
			"deferred-link": { // declares links but is not installed — must be skipped, no error
				Node:  &binmanager.AppConfigNode{PackageName: "p2"},
				Lazy:  true,
				Links: map[string]string{"deferred.config.js": "config.js"},
			},
		},
	}
	resolver := &mockInstallRootResolver{paths: map[string]string{"installed-link": installedDir}}

	created, err := materializeInstalledLinks(gitRoot, cfg, resolver, nil, false)
	if err != nil {
		t.Fatalf("a deferred/uninstalled link-app must not error the link pass, got: %v", err)
	}
	if len(created) != 1 || created[0] != "installed.config.js" {
		t.Fatalf("created = %v, want [installed.config.js]", created)
	}
	if _, err := os.Lstat(filepath.Join(gitRoot, ".datamitsu", "installed.config.js")); err != nil {
		t.Errorf("expected installed app's link to exist: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(gitRoot, ".datamitsu", "deferred.config.js")); !os.IsNotExist(err) {
		t.Errorf("expected deferred app's link to be absent, got err=%v", err)
	}
}

// TestMaterializeInstalledLinks_AllDeferredWritesTypeDefs: when every configured
// link-app is deferred (none installed), no symlinks are created but .datamitsu/
// type definitions are still written and there is no error.
func TestMaterializeInstalledLinks_AllDeferredWritesTypeDefs(t *testing.T) {
	gitRoot := filepath.Join(t.TempDir(), "repo")
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"slidev": {
				Node:  &binmanager.AppConfigNode{PackageName: "p"},
				Lazy:  true,
				Links: map[string]string{"theme": "t"},
			},
		},
	}
	resolver := &mockInstallRootResolver{paths: map[string]string{}} // nothing installed

	created, err := materializeInstalledLinks(gitRoot, cfg, resolver, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("want no links created, got %v", created)
	}
	if _, err := os.Stat(filepath.Join(gitRoot, ".datamitsu", "datamitsu.config.d.ts")); err != nil {
		t.Errorf("expected type defs written even when all link-apps deferred: %v", err)
	}
}

// TestMaterializeInstalledLinks_NoLinksWritesTypeDefs: no links configured at all
// — only type defs are laid down. Also exercises the nil-resolver fast path.
func TestMaterializeInstalledLinks_NoLinksWritesTypeDefs(t *testing.T) {
	gitRoot := filepath.Join(t.TempDir(), "repo")
	cfg := &config.Config{Apps: binmanager.MapOfApps{"x": {Binary: &binmanager.AppConfigBinary{}}}}

	created, err := materializeInstalledLinks(gitRoot, cfg, nil, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("want no links, got %v", created)
	}
	if _, err := os.Stat(filepath.Join(gitRoot, ".datamitsu", "datamitsu.config.d.ts")); err != nil {
		t.Errorf("expected type defs: %v", err)
	}
}

// TestMaterializeInstalledLinks_DryRun: dry-run reports the would-be links for
// installed apps, skips deferred ones without error, and touches no filesystem.
func TestMaterializeInstalledLinks_DryRun(t *testing.T) {
	tmp := t.TempDir()
	gitRoot := filepath.Join(tmp, "repo")
	installedDir := filepath.Join(tmp, "store", "h1")
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installedDir, "config.js"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"installed-link": {
				Node:  &binmanager.AppConfigNode{PackageName: "p"},
				Links: map[string]string{"c.js": "config.js"},
			},
			"deferred-link": {
				Node:  &binmanager.AppConfigNode{PackageName: "p2"},
				Lazy:  true,
				Links: map[string]string{"d.js": "config.js"},
			},
		},
	}
	resolver := &mockInstallRootResolver{paths: map[string]string{"installed-link": installedDir}}

	created, err := materializeInstalledLinks(gitRoot, cfg, resolver, nil, true)
	if err != nil {
		t.Fatalf("dry-run must not error on a deferred app: %v", err)
	}
	if len(created) != 1 || created[0] != "c.js" {
		t.Fatalf("created = %v, want [c.js]", created)
	}
	if _, err := os.Lstat(filepath.Join(gitRoot, ".datamitsu", "c.js")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create the symlink, got err=%v", err)
	}
}

// TestMaterializeInstalledLinks_LinksInstalledBundle covers the bundle branch:
// an installed bundle that declares links is materialized under .datamitsu/.
func TestMaterializeInstalledLinks_LinksInstalledBundle(t *testing.T) {
	tmp := t.TempDir()
	gitRoot := filepath.Join(tmp, "repo")
	bundleDir := filepath.Join(tmp, "store", "bundle", "h1")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "schema.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Bundles: binmanager.MapOfBundles{
			"my-bundle": {Links: map[string]string{"schema.json": "schema.json"}},
		},
	}
	appResolver := &mockInstallRootResolver{paths: map[string]string{}}
	bundleResolver := &mockInstallRootResolver{paths: map[string]string{"my-bundle": bundleDir}}

	created, err := materializeInstalledLinks(gitRoot, cfg, appResolver, bundleResolver, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 1 || created[0] != "schema.json" {
		t.Fatalf("created = %v, want [schema.json]", created)
	}
	if _, err := os.Lstat(filepath.Join(gitRoot, ".datamitsu", "schema.json")); err != nil {
		t.Errorf("expected bundle link to exist: %v", err)
	}
}

func TestBundleResolverFor(t *testing.T) {
	if r := bundleResolverFor(&config.Config{}, nil); r != nil {
		t.Errorf("no bundles → want nil resolver, got %v", r)
	}
	cfg := &config.Config{Bundles: binmanager.MapOfBundles{
		"b": {Links: map[string]string{"x": "x"}},
	}}
	if r := bundleResolverFor(cfg, nil); r == nil {
		t.Error("bundles present → want non-nil resolver")
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

	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "agents.md"), []byte("# Agents"), 0o644); err != nil {
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

	_, _, err := reportBundles(ctx, ui.New(term.Plain), bm, false)
	if err != nil {
		t.Fatalf("reportBundles failed: %v", err)
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

	_, _, err := reportBundles(ctx, ui.New(term.Plain), bm, true)
	if err != nil {
		t.Fatalf("reportBundles failed: %v", err)
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

		if err := os.MkdirAll(installDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(installDir, "config.js"), []byte("// config"), 0o644); err != nil {
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

		if err := os.MkdirAll(installDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(installDir, "config.js"), []byte("// config"), 0o644); err != nil {
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
		if err := os.WriteFile(dtsPath, []byte("// stale content"), 0o644); err != nil {
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
