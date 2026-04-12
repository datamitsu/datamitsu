package binmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleDeserialize(t *testing.T) {
	jsonData := `{
		"version": "1.0",
		"files": {
			"agents.md": "# Agent instructions",
			"skills/search.md": "# Search skill"
		},
		"links": {
			"agent-skills-dir": ".",
			"agents-md": "agents.md"
		}
	}`

	var bundle Bundle
	if err := json.Unmarshal([]byte(jsonData), &bundle); err != nil {
		t.Fatalf("failed to unmarshal bundle: %v", err)
	}

	if bundle.Version != "1.0" {
		t.Errorf("Version = %q, want %q", bundle.Version, "1.0")
	}
	if len(bundle.Files) != 2 {
		t.Errorf("Files count = %d, want 2", len(bundle.Files))
	}
	if bundle.Files["agents.md"] != "# Agent instructions" {
		t.Errorf("Files[agents.md] = %q, want %q", bundle.Files["agents.md"], "# Agent instructions")
	}
	if len(bundle.Links) != 2 {
		t.Errorf("Links count = %d, want 2", len(bundle.Links))
	}
	if bundle.Links["agent-skills-dir"] != "." {
		t.Errorf("Links[agent-skills-dir] = %q, want %q", bundle.Links["agent-skills-dir"], ".")
	}
}

func TestBundleDeserializeWithArchives(t *testing.T) {
	jsonData := `{
		"version": "2.0",
		"archives": {
			"dist": {
				"url": "https://example.com/dist.tar.gz",
				"hash": "abc123",
				"format": "tar.gz"
			}
		},
		"links": {
			"my-dist": "dist"
		}
	}`

	var bundle Bundle
	if err := json.Unmarshal([]byte(jsonData), &bundle); err != nil {
		t.Fatalf("failed to unmarshal bundle: %v", err)
	}

	if bundle.Version != "2.0" {
		t.Errorf("Version = %q, want %q", bundle.Version, "2.0")
	}
	if len(bundle.Archives) != 1 {
		t.Errorf("Archives count = %d, want 1", len(bundle.Archives))
	}
	distArchive := bundle.Archives["dist"]
	if distArchive == nil {
		t.Fatal("Archives[dist] is nil")
	}
	if distArchive.URL != "https://example.com/dist.tar.gz" {
		t.Errorf("Archives[dist].URL = %q", distArchive.URL)
	}
}

func TestBundleDeserializeEmpty(t *testing.T) {
	jsonData := `{}`

	var bundle Bundle
	if err := json.Unmarshal([]byte(jsonData), &bundle); err != nil {
		t.Fatalf("failed to unmarshal bundle: %v", err)
	}

	if bundle.Version != "" {
		t.Errorf("Version = %q, want empty", bundle.Version)
	}
	if bundle.Files != nil {
		t.Errorf("Files = %v, want nil", bundle.Files)
	}
	if bundle.Archives != nil {
		t.Errorf("Archives = %v, want nil", bundle.Archives)
	}
	if bundle.Links != nil {
		t.Errorf("Links = %v, want nil", bundle.Links)
	}
}

func TestMapOfBundlesDeserialize(t *testing.T) {
	jsonData := `{
		"agent-skills": {
			"version": "1.0",
			"files": {"agents.md": "content"},
			"links": {"agents": "agents.md"}
		},
		"templates": {
			"version": "2.0",
			"files": {"template.txt": "hello"}
		}
	}`

	var bundles MapOfBundles
	if err := json.Unmarshal([]byte(jsonData), &bundles); err != nil {
		t.Fatalf("failed to unmarshal bundles: %v", err)
	}

	if len(bundles) != 2 {
		t.Errorf("bundles count = %d, want 2", len(bundles))
	}

	agentSkills := bundles["agent-skills"]
	if agentSkills == nil {
		t.Fatal("bundles[agent-skills] is nil")
	}
	if agentSkills.Version != "1.0" {
		t.Errorf("agent-skills.Version = %q, want %q", agentSkills.Version, "1.0")
	}

	templates := bundles["templates"]
	if templates == nil {
		t.Fatal("bundles[templates] is nil")
	}
	if templates.Version != "2.0" {
		t.Errorf("templates.Version = %q, want %q", templates.Version, "2.0")
	}
}

func TestBinManagerInitializesWithBundles(t *testing.T) {
	bundles := MapOfBundles{
		"test-bundle": {
			Version: "1.0",
			Files:   map[string]string{"a.txt": "hello"},
			Links:   map[string]string{"a": "a.txt"},
		},
	}

	bm := New(MapOfApps{}, bundles, nil)

	if bm.mapOfBundles == nil {
		t.Fatal("mapOfBundles is nil")
	}
	if len(bm.mapOfBundles) != 1 {
		t.Errorf("mapOfBundles count = %d, want 1", len(bm.mapOfBundles))
	}
	if bm.mapOfBundles["test-bundle"] == nil {
		t.Fatal("mapOfBundles[test-bundle] is nil")
	}
	if bm.mapOfBundles["test-bundle"].Version != "1.0" {
		t.Errorf("bundle version = %q, want %q", bm.mapOfBundles["test-bundle"].Version, "1.0")
	}
}

func TestBinManagerInitializesWithNilBundles(t *testing.T) {
	bm := New(MapOfApps{}, nil, nil)

	if bm.mapOfBundles != nil {
		t.Errorf("mapOfBundles = %v, want nil", bm.mapOfBundles)
	}
}

func TestHasExternalArchives(t *testing.T) {
	t.Run("no archives", func(t *testing.T) {
		b := &Bundle{Files: map[string]string{"a": "b"}}
		if b.HasExternalArchives() {
			t.Error("expected false for bundle with no archives")
		}
	})

	t.Run("inline only", func(t *testing.T) {
		b := &Bundle{Archives: map[string]*ArchiveSpec{
			"a": {Inline: "tar.br:abc"},
		}}
		if b.HasExternalArchives() {
			t.Error("expected false for inline-only archives")
		}
	})

	t.Run("external archive", func(t *testing.T) {
		b := &Bundle{Archives: map[string]*ArchiveSpec{
			"a": {URL: "https://example.com/a.tar.gz", Hash: "abc", Format: "tar.gz"},
		}}
		if !b.HasExternalArchives() {
			t.Error("expected true for external archive")
		}
	})
}

func TestInstallBundle_FreshInstall(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := MapOfBundles{
		"test-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"hello.txt":     "hello world",
				"sub/nested.md": "# Nested",
			},
			Links: map[string]string{"hello": "hello.txt"},
		},
	}

	bm := New(MapOfApps{}, bundles, nil)
	ctx := context.Background()

	err := bm.installBundle(ctx, "test-bundle")
	if err != nil {
		t.Fatalf("installBundle failed: %v", err)
	}

	path, err := bm.ComputeBundlePath("test-bundle")
	if err != nil {
		t.Fatalf("ComputeBundlePath failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(path, "hello.txt"))
	if err != nil {
		t.Fatalf("failed to read hello.txt: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("hello.txt = %q, want %q", string(content), "hello world")
	}

	content, err = os.ReadFile(filepath.Join(path, "sub", "nested.md"))
	if err != nil {
		t.Fatalf("failed to read sub/nested.md: %v", err)
	}
	if string(content) != "# Nested" {
		t.Errorf("sub/nested.md = %q, want %q", string(content), "# Nested")
	}
}

func TestInstallBundle_AlreadyCached(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := MapOfBundles{
		"cached-bundle": {
			Version: "1.0",
			Files:   map[string]string{"a.txt": "original"},
		},
	}

	bm := New(MapOfApps{}, bundles, nil)
	ctx := context.Background()

	// First install
	if err := bm.installBundle(ctx, "cached-bundle"); err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	path, _ := bm.ComputeBundlePath("cached-bundle")

	// Overwrite file to detect if second install overwrites
	if err := os.WriteFile(filepath.Join(path, "a.txt"), []byte("modified"), 0644); err != nil {
		t.Fatalf("failed to modify file: %v", err)
	}

	// Second install should be a no-op (already cached)
	if err := bm.installBundle(ctx, "cached-bundle"); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(path, "a.txt"))
	if string(content) != "modified" {
		t.Error("second install overwrote cached content; expected no-op")
	}
}

func TestInstallBundle_NotFound(t *testing.T) {
	bm := New(MapOfApps{}, MapOfBundles{}, nil)
	ctx := context.Background()

	err := bm.installBundle(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent bundle")
	}
}

func TestInstallBundles_Empty(t *testing.T) {
	bm := New(MapOfApps{}, MapOfBundles{}, nil)
	ctx := context.Background()

	stats, err := bm.InstallBundles(ctx, false)
	if err != nil {
		t.Fatalf("InstallBundles failed: %v", err)
	}
	if len(stats.Installed) != 0 || len(stats.AlreadyCached) != 0 || len(stats.Failed) != 0 {
		t.Errorf("expected empty stats, got installed=%d cached=%d failed=%d",
			len(stats.Installed), len(stats.AlreadyCached), len(stats.Failed))
	}
}

func TestInstallBundles_MultipleBundles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := MapOfBundles{
		"bundle-a": {
			Version: "1.0",
			Files:   map[string]string{"a.txt": "aaa"},
		},
		"bundle-b": {
			Version: "2.0",
			Files:   map[string]string{"b.txt": "bbb"},
		},
	}

	bm := New(MapOfApps{}, bundles, nil)
	ctx := context.Background()

	stats, err := bm.InstallBundles(ctx, false)
	if err != nil {
		t.Fatalf("InstallBundles failed: %v", err)
	}
	if len(stats.Installed) != 2 {
		t.Errorf("Installed count = %d, want 2", len(stats.Installed))
	}
	if len(stats.AlreadyCached) != 0 {
		t.Errorf("AlreadyCached count = %d, want 0", len(stats.AlreadyCached))
	}

	// Run again — should all be cached
	stats2, err := bm.InstallBundles(ctx, false)
	if err != nil {
		t.Fatalf("second InstallBundles failed: %v", err)
	}
	if len(stats2.AlreadyCached) != 2 {
		t.Errorf("second run: AlreadyCached count = %d, want 2", len(stats2.AlreadyCached))
	}
	if len(stats2.Installed) != 0 {
		t.Errorf("second run: Installed count = %d, want 0", len(stats2.Installed))
	}
}

func TestInstallBundles_SkipExternalArchives(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := MapOfBundles{
		"inline-only": {
			Version: "1.0",
			Files:   map[string]string{"a.txt": "inline content"},
		},
		"with-external": {
			Version: "1.0",
			Files:   map[string]string{"b.txt": "has external"},
			Archives: map[string]*ArchiveSpec{
				"dist": {URL: "https://example.com/dist.tar.gz", Hash: "abc123", Format: "tar.gz"},
			},
		},
	}

	bm := New(MapOfApps{}, bundles, nil)
	ctx := context.Background()

	stats, err := bm.InstallBundles(ctx, true)
	if err != nil {
		t.Fatalf("InstallBundles failed: %v", err)
	}

	if len(stats.Installed) != 1 || stats.Installed[0] != "inline-only" {
		t.Errorf("Installed = %v, want [inline-only]", stats.Installed)
	}
	if len(stats.Skipped) != 1 || stats.Skipped[0] != "with-external" {
		t.Errorf("Skipped = %v, want [with-external]", stats.Skipped)
	}
}

func TestInstallBundles_NoSkipInstallsAll(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := MapOfBundles{
		"inline-only": {
			Version: "1.0",
			Files:   map[string]string{"a.txt": "inline content"},
		},
	}

	bm := New(MapOfApps{}, bundles, nil)
	ctx := context.Background()

	stats, err := bm.InstallBundles(ctx, false)
	if err != nil {
		t.Fatalf("InstallBundles failed: %v", err)
	}

	if len(stats.Installed) != 1 {
		t.Errorf("Installed count = %d, want 1", len(stats.Installed))
	}
	if len(stats.Skipped) != 0 {
		t.Errorf("Skipped count = %d, want 0", len(stats.Skipped))
	}
}

func TestInstallBundles_ContextCancelled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	bundles := MapOfBundles{
		"bundle-a": {Version: "1.0", Files: map[string]string{"a.txt": "aaa"}},
	}

	bm := New(MapOfApps{}, bundles, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := bm.InstallBundles(ctx, false)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
