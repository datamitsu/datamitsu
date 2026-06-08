package ocidigest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDigestCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := digestCachePath(dir, "ghcr.io", "datamitsu/datamitsu", "1.2.3")

	if err := saveCachedDigest(path, "ghcr.io", "datamitsu/datamitsu", "1.2.3", testDigest); err != nil {
		t.Fatalf("saveCachedDigest: %v", err)
	}

	got, ok := loadCachedDigest(path)
	if !ok {
		t.Fatal("loadCachedDigest reported miss after save")
	}
	if got != testDigest {
		t.Errorf("digest = %q, want %q", got, testDigest)
	}
}

func TestDigestCache_MissingFile(t *testing.T) {
	path := digestCachePath(t.TempDir(), "ghcr.io", "x/y", "1.0.0")
	if _, ok := loadCachedDigest(path); ok {
		t.Error("expected miss for non-existent cache file")
	}
}

func TestDigestCache_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := digestCachePath(dir, "ghcr.io", "x/y", "1.0.0")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := loadCachedDigest(path); ok {
		t.Error("expected miss for corrupt cache file")
	}
}

func TestDigestCachePath_StableAndDistinct(t *testing.T) {
	a := digestCachePath("/c", "ghcr.io", "datamitsu/datamitsu", "1.2.3")
	b := digestCachePath("/c", "ghcr.io", "datamitsu/datamitsu", "1.2.3")
	if a != b {
		t.Errorf("path not stable: %q != %q", a, b)
	}
	c := digestCachePath("/c", "ghcr.io", "datamitsu/datamitsu", "1.2.4")
	if a == c {
		t.Error("different tags produced the same cache path")
	}
}

func TestSaveCachedDigest_NoTempResidue(t *testing.T) {
	dir := t.TempDir()
	path := digestCachePath(dir, "ghcr.io", "x/y", "1.0.0")
	if err := saveCachedDigest(path, "ghcr.io", "x/y", "1.0.0", testDigest); err != nil {
		t.Fatalf("saveCachedDigest: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
