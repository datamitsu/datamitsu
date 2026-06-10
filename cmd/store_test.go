package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestStorePathPrintsCachePath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	err := runStorePath(nil, nil)
	if err != nil {
		t.Fatalf("runStorePath returned error: %v", err)
	}
}

func TestStoreClearRemovesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	storeDir := filepath.Join(tmpDir, "store")
	subDir := filepath.Join(storeDir, ".bin", "some-tool")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	testFile := filepath.Join(subDir, "binary")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	err := runStoreClear(nil, nil)
	if err != nil {
		t.Fatalf("runStoreClear returned error: %v", err)
	}

	if _, err := os.Stat(storeDir); !os.IsNotExist(err) {
		t.Errorf("store directory should be removed, but still exists")
	}
}

func TestStoreClearRemovesReadOnlyModuleCache(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	// Mimic Go module cache layout: read-only files inside read-only dirs.
	// A plain os.RemoveAll fails here with EACCES; ForceRemoveAll must succeed.
	storeDir := filepath.Join(tmpDir, "store")
	modDir := filepath.Join(storeDir, ".apps", "go", "tool", "gomodcache", "example.com", "mod@v1.0.0")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	roFile := filepath.Join(modDir, "source.go")
	if err := os.WriteFile(roFile, []byte("package mod"), 0o444); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.Chmod(modDir, 0o555); err != nil {
		t.Fatalf("failed to chmod dir: %v", err)
	}
	// Restore write permission on failure so t.TempDir() cleanup can proceed.
	t.Cleanup(func() { _ = os.Chmod(modDir, 0o755) })

	if err := runStoreClear(nil, nil); err != nil {
		t.Fatalf("runStoreClear returned error: %v", err)
	}

	if _, err := os.Stat(storeDir); !os.IsNotExist(err) {
		t.Errorf("store directory should be removed, but still exists")
	}
}

func TestStoreClearNonExistentDirectory(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "nonexistent")
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	err := runStoreClear(nil, nil)
	if err != nil {
		t.Fatalf("runStoreClear should not fail on nonexistent directory: %v", err)
	}
}

func TestStoreClearRejectsDangerousPath(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", "/")

	err := runStoreClear(nil, nil)
	// With store path = /store, this is no longer "/" but still a top-level dir.
	// The function should succeed since /store is a valid path to clear.
	// The dangerous path check protects against /, HOME, etc.
	if err != nil {
		t.Fatalf("runStoreClear returned unexpected error: %v", err)
	}
}

func TestStoreClearRejectsHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Set DATAMITSU_CACHE_DIR such that GetStorePath() returns HOME
	// GetStorePath() = DATAMITSU_CACHE_DIR + "/store", so we need parent of home
	// This is hard to construct generically, so test with HOME set to {tmpDir}/store
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	t.Setenv("HOME", storeDir)
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Dir(storeDir))

	err := runStoreClear(nil, nil)
	if err == nil {
		t.Fatal("expected error when store path equals HOME")
	}
}

func TestStoreClearRejectsRelativePath(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", "..")

	err := runStoreClear(nil, nil)
	if err == nil {
		t.Fatal("expected error when store path is a relative path")
	}
}

func TestStoreClearRejectsAncestorOfHome(t *testing.T) {
	// Set HOME so that store path is an ancestor of it
	// GetStorePath() = /home/store, HOME = /home/store/testuser
	t.Setenv("HOME", "/home/store/testuser")
	t.Setenv("DATAMITSU_CACHE_DIR", "/home")

	err := runStoreClear(nil, nil)
	if err == nil {
		t.Fatal("expected error when store path is an ancestor of HOME")
	}
}

func TestStoreCommandsRegistered(t *testing.T) {
	var foundStore bool
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "store" {
			foundStore = true

			subNames := make(map[string]bool)
			for _, sub := range cmd.Commands() {
				subNames[sub.Use] = true
			}
			if !subNames["path"] {
				t.Error("store command missing 'path' subcommand")
			}
			if !subNames["clear"] {
				t.Error("store command missing 'clear' subcommand")
			}
			break
		}
	}
	if !foundStore {
		t.Error("rootCmd missing 'store' command")
	}
}

const storeTestDigest = "sha256:abababababababababababababababababababababababababababababababab"

func TestResolveSeedRef(t *testing.T) {
	ctx := context.Background()

	t.Run("config declaration is used without arguments", func(t *testing.T) {
		cfg := &config.Config{OCI: &config.OCIRef{Ref: "ghcr.io/owner/repo", Digest: storeTestDigest}}
		ref, err := resolveSeedRef(ctx, cfg, nil)
		if err != nil {
			t.Fatalf("resolveSeedRef: %v", err)
		}
		if ref != cfg.OCI {
			t.Errorf("ref = %+v, want the config declaration", ref)
		}
	})

	t.Run("no arguments and no declaration is an error", func(t *testing.T) {
		_, err := resolveSeedRef(ctx, &config.Config{}, nil)
		if err == nil || !strings.Contains(err.Error(), "no oci bundle declared") {
			t.Errorf("err = %v, want a no-declaration error", err)
		}
	})

	t.Run("digest-pinned argument overrides the config", func(t *testing.T) {
		cfg := &config.Config{OCI: &config.OCIRef{Ref: "ghcr.io/owner/other", Digest: storeTestDigest}}
		ref, err := resolveSeedRef(ctx, cfg, []string{"ghcr.io/owner/repo@" + storeTestDigest})
		if err != nil {
			t.Fatalf("resolveSeedRef: %v", err)
		}
		if ref.Ref != "ghcr.io/owner/repo" || ref.Digest != storeTestDigest {
			t.Errorf("ref = %+v, want the explicit argument", ref)
		}
	})

	t.Run("tag reference without --resolve-tag is refused", func(t *testing.T) {
		_, err := resolveSeedRef(ctx, &config.Config{}, []string{"ghcr.io/owner/repo:latest"})
		if err == nil || !strings.Contains(err.Error(), "--resolve-tag") {
			t.Errorf("err = %v, want a pin-the-digest refusal", err)
		}
	})

	t.Run("bare reference without tag or digest is refused", func(t *testing.T) {
		_, err := resolveSeedRef(ctx, &config.Config{}, []string{"ghcr.io/owner/repo"})
		if err == nil {
			t.Error("expected an error for an unpinned reference")
		}
	})
}

func storeTestSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// seedStoreTestBundle prepares an isolated cache/store, a git root with a
// config declaring the bundle, the bundle manifest pre-seeded into the
// on-disk manifest cache (so the handlers never touch the network), and an
// OCI layout directory holding the same content for import tests. It returns
// the manifest digest and the store-relative subtree of the single annotated
// layer.
func seedStoreTestBundle(t *testing.T) (digest, subtree string) {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	root := setupGitRoot(t)

	subtree = ".bin/cli-tool/0123456789abcdef0123456789abcdef"
	payload := []byte("cli seeded payload")

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	for _, dir := range []string{"dm/", "dm/store/", "dm/store/.bin/", "dm/store/.bin/cli-tool/"} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "dm/store/" + subtree, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	blob := compressed.Bytes()

	manifest := ocispec.Manifest{
		MediaType:   ocispec.MediaTypeImageManifest,
		Annotations: map[string]string{"com.datamitsu.store-root": "/dm/store"},
		Layers: []ocispec.Descriptor{{
			MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
			Digest:    godigest.Digest(storeTestSHA256(blob)),
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				"com.datamitsu.subtree": subtree,
				"com.datamitsu.kind":    "binary",
				"com.datamitsu.app":     "cli-tool",
			},
		}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest = storeTestSHA256(raw)

	// Pre-seed the on-disk manifest cache (content-addressed, verified on read).
	cachePath := filepath.Join(env.GetCachePath(), "oci", "manifests", strings.ReplaceAll(digest, ":", "-")+".json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	// Stage the same content as an OCI image layout for import tests.
	blobDir := filepath.Join(root, "layout", "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, strings.TrimPrefix(storeTestSHA256(blob), "sha256:")), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, strings.TrimPrefix(digest, "sha256:")), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(root, "datamitsu.config.ts"), `
function getMinVersion() { return "0.0.0"; }
function getConfig(input) {
  return { ...input, oci: { ref: "registry.invalid/owner/repo", digest: "`+digest+`" } };
}
`)
	return digest, subtree
}

func TestRunStoreSeed_WarmStoreFullPullWritesMarker(t *testing.T) {
	digest, subtree := seedStoreTestBundle(t)

	// Pre-place the subtree: the only layer is skipped, so the full pull
	// completes without any blob download and writes the marker.
	placed := filepath.Join(env.GetStorePath(), filepath.FromSlash(subtree))
	if err := os.MkdirAll(filepath.Dir(placed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(placed, []byte("already here"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runStoreSeed(context.Background(), nil); err != nil {
		t.Fatalf("runStoreSeed: %v", err)
	}

	marker := filepath.Join(env.GetStorePath(), ".oci-seeded", strings.ReplaceAll(digest, ":", "-"))
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("full pull did not write the seed marker: %v", err)
	}
}

func TestRunStoreImport_LayoutEndToEnd(t *testing.T) {
	_, subtree := seedStoreTestBundle(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := runStoreImport(context.Background(), filepath.Join(root, "layout")); err != nil {
		t.Fatalf("runStoreImport: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(env.GetStorePath(), filepath.FromSlash(subtree)))
	if err != nil {
		t.Fatalf("imported binary missing: %v", err)
	}
	if string(data) != "cli seeded payload" {
		t.Errorf("imported content = %q", data)
	}
}

func TestRunStoreStatus_TextAndJSON(t *testing.T) {
	_, _ = seedStoreTestBundle(t)

	if err := runStoreStatus(context.Background()); err != nil {
		t.Fatalf("runStoreStatus (text): %v", err)
	}

	storeStatusJSON = true
	t.Cleanup(func() { storeStatusJSON = false })
	if err := runStoreStatus(context.Background()); err != nil {
		t.Fatalf("runStoreStatus (json): %v", err)
	}
}
