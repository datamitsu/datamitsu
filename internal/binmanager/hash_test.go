package binmanager

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/target"
)

func makeResolvedTarget(os, arch string, libc target.LibcType) target.ResolvedTarget {
	return target.ResolvedTarget{
		Target: target.Target{OS: os, Arch: arch, Libc: libc},
		Source: target.ResolutionExact,
	}
}

func TestCalculateConfigHash(t *testing.T) {
	binaryPath := "bin/app"

	tests := []struct {
		name     string
		info     BinaryOsArchInfo
		resolved target.ResolvedTarget
	}{
		{
			name: "basic config",
			info: BinaryOsArchInfo{
				URL:         "https://example.com/app.tar.gz",
				Hash:        "abc123",
				ContentType: BinContentTypeTarGz,
			},
			resolved: makeResolvedTarget("darwin", "amd64", target.LibcUnknown),
		},
		{
			name: "with binary path",
			info: BinaryOsArchInfo{
				URL:         "https://example.com/app.tar.gz",
				Hash:        "abc123",
				ContentType: BinContentTypeTarGz,
				BinaryPath:  &binaryPath,
			},
			resolved: makeResolvedTarget("linux", "arm64", target.LibcGlibc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := calculateConfigHash(tt.info, tt.resolved)

			if hash == "" {
				t.Error("hash is empty")
			}

			if len(hash) != 32 {
				t.Errorf("hash length = %d, want 32 (xxh3-128)", len(hash))
			}
		})
	}

	t.Run("deterministic hash", func(t *testing.T) {
		info := BinaryOsArchInfo{
			URL:         "https://example.com/app.tar.gz",
			Hash:        "abc123",
			ContentType: BinContentTypeTarGz,
		}
		resolved := makeResolvedTarget("darwin", "amd64", target.LibcUnknown)

		hash1 := calculateConfigHash(info, resolved)
		hash2 := calculateConfigHash(info, resolved)

		if hash1 != hash2 {
			t.Error("hash is not deterministic")
		}
	})

	t.Run("different configs produce different hashes", func(t *testing.T) {
		info1 := BinaryOsArchInfo{
			URL:         "https://example.com/app1.tar.gz",
			Hash:        "abc123",
			ContentType: BinContentTypeTarGz,
		}

		info2 := BinaryOsArchInfo{
			URL:         "https://example.com/app2.tar.gz",
			Hash:        "abc123",
			ContentType: BinContentTypeTarGz,
		}

		resolved := makeResolvedTarget("darwin", "amd64", target.LibcUnknown)
		hash1 := calculateConfigHash(info1, resolved)
		hash2 := calculateConfigHash(info2, resolved)

		if hash1 == hash2 {
			t.Error("different configs produced same hash")
		}
	})

	t.Run("different os/arch produce different hashes", func(t *testing.T) {
		info := BinaryOsArchInfo{
			URL:         "https://example.com/app.tar.gz",
			Hash:        "abc123",
			ContentType: BinContentTypeTarGz,
		}

		hash1 := calculateConfigHash(info, makeResolvedTarget("darwin", "amd64", target.LibcUnknown))
		hash2 := calculateConfigHash(info, makeResolvedTarget("linux", "amd64", target.LibcGlibc))
		hash3 := calculateConfigHash(info, makeResolvedTarget("darwin", "arm64", target.LibcUnknown))

		if hash1 == hash2 {
			t.Error("different OS produced same hash")
		}

		if hash1 == hash3 {
			t.Error("different arch produced same hash")
		}
	})

	t.Run("musl binary produces different hash than glibc", func(t *testing.T) {
		info := BinaryOsArchInfo{
			URL:         "https://example.com/app.tar.gz",
			Hash:        "abc123",
			ContentType: BinContentTypeTarGz,
		}

		hashMusl := calculateConfigHash(info, makeResolvedTarget("linux", "amd64", target.LibcMusl))
		hashGlibc := calculateConfigHash(info, makeResolvedTarget("linux", "amd64", target.LibcGlibc))

		if hashMusl == hashGlibc {
			t.Error("musl and glibc resolved targets should produce different cache hashes")
		}
	})

	t.Run("glibc fallback uses glibc hash not musl", func(t *testing.T) {
		info := BinaryOsArchInfo{
			URL:         "https://example.com/app.tar.gz",
			Hash:        "abc123",
			ContentType: BinContentTypeTarGz,
		}

		glibcExact := makeResolvedTarget("linux", "amd64", target.LibcGlibc)
		glibcFallback := target.ResolvedTarget{
			Target: target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc},
			Source: target.ResolutionFallback,
			FallbackInfo: &target.FallbackInfo{
				RequestedTarget: target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcMusl},
				Reason:          "musl binary not available",
			},
		}

		hashExact := calculateConfigHash(info, glibcExact)
		hashFallback := calculateConfigHash(info, glibcFallback)

		if hashExact != hashFallback {
			t.Error("fallback to glibc should produce same hash as exact glibc match")
		}
	})

	t.Run("different resolved targets produce different cache paths", func(t *testing.T) {
		info := BinaryOsArchInfo{
			URL:         "https://example.com/app.tar.gz",
			Hash:        "abc123",
			ContentType: BinContentTypeTarGz,
		}

		targets := []target.ResolvedTarget{
			makeResolvedTarget("linux", "amd64", target.LibcGlibc),
			makeResolvedTarget("linux", "amd64", target.LibcMusl),
			makeResolvedTarget("linux", "amd64", target.LibcUnknown),
			makeResolvedTarget("darwin", "amd64", target.LibcUnknown),
			makeResolvedTarget("linux", "arm64", target.LibcGlibc),
		}

		hashes := make(map[string]bool)
		for _, rt := range targets {
			h := calculateConfigHash(info, rt)
			if hashes[h] {
				t.Errorf("duplicate hash for target %s", rt.Target.String())
			}
			hashes[h] = true
		}
	})
}

func TestHashFilesAndArchives(t *testing.T) {
	t.Run("empty inputs returns empty string", func(t *testing.T) {
		result := HashFilesAndArchives(nil, nil)
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("deterministic with files", func(t *testing.T) {
		files := map[string]string{"a.txt": "hello", "b.txt": "world"}
		hash1 := HashFilesAndArchives(files, nil)
		hash2 := HashFilesAndArchives(files, nil)
		if hash1 != hash2 {
			t.Error("hash is not deterministic")
		}
		if len(hash1) != 32 {
			t.Errorf("hash length = %d, want 32", len(hash1))
		}
	})

	t.Run("different files produce different hashes", func(t *testing.T) {
		files1 := map[string]string{"a.txt": "hello"}
		files2 := map[string]string{"a.txt": "world"}
		hash1 := HashFilesAndArchives(files1, nil)
		hash2 := HashFilesAndArchives(files2, nil)
		if hash1 == hash2 {
			t.Error("different files produced same hash")
		}
	})

	t.Run("with archives", func(t *testing.T) {
		archives := map[string]*ArchiveSpec{
			"dist": {Inline: "tar.br:abc123"},
		}
		hash := HashFilesAndArchives(nil, archives)
		if hash == "" {
			t.Error("expected non-empty hash")
		}
	})

	t.Run("nil archive spec skipped", func(t *testing.T) {
		archives := map[string]*ArchiveSpec{
			"dist": nil,
		}
		hash := HashFilesAndArchives(nil, archives)
		if hash == "" {
			t.Error("expected non-empty hash for non-empty map even with nil spec")
		}
	})
}

func TestCalculateBundleHash(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		files := map[string]string{"a.txt": "content"}
		hash1 := calculateBundleHash("mybundle", "1.0", files, nil)
		hash2 := calculateBundleHash("mybundle", "1.0", files, nil)
		if hash1 != hash2 {
			t.Error("hash is not deterministic")
		}
		if len(hash1) != 32 {
			t.Errorf("hash length = %d, want 32", len(hash1))
		}
	})

	t.Run("different name produces different hash", func(t *testing.T) {
		files := map[string]string{"a.txt": "content"}
		hash1 := calculateBundleHash("bundle-a", "1.0", files, nil)
		hash2 := calculateBundleHash("bundle-b", "1.0", files, nil)
		if hash1 == hash2 {
			t.Error("different names produced same hash")
		}
	})

	t.Run("different version produces different hash", func(t *testing.T) {
		files := map[string]string{"a.txt": "content"}
		hash1 := calculateBundleHash("mybundle", "1.0", files, nil)
		hash2 := calculateBundleHash("mybundle", "2.0", files, nil)
		if hash1 == hash2 {
			t.Error("different versions produced same hash")
		}
	})

	t.Run("different files produces different hash", func(t *testing.T) {
		files1 := map[string]string{"a.txt": "hello"}
		files2 := map[string]string{"a.txt": "world"}
		hash1 := calculateBundleHash("mybundle", "1.0", files1, nil)
		hash2 := calculateBundleHash("mybundle", "1.0", files2, nil)
		if hash1 == hash2 {
			t.Error("different files produced same hash")
		}
	})

	t.Run("empty files and archives", func(t *testing.T) {
		hash := calculateBundleHash("mybundle", "1.0", nil, nil)
		if hash == "" {
			t.Error("expected non-empty hash even with no files/archives")
		}
	})
}

func TestComputeBundlePath(t *testing.T) {
	t.Run("returns path for existing bundle", func(t *testing.T) {
		bundles := MapOfBundles{
			"test-bundle": {
				Version: "1.0",
				Files:   map[string]string{"a.txt": "hello"},
			},
		}
		bm := New(MapOfApps{}, bundles, nil)

		path, err := bm.ComputeBundlePath("test-bundle")
		if err != nil {
			t.Fatalf("ComputeBundlePath() error = %v", err)
		}
		if path == "" {
			t.Error("expected non-empty path")
		}
		if !strings.Contains(path, ".bundles") {
			t.Errorf("path %q does not contain .bundles", path)
		}
		if !strings.Contains(path, "test-bundle") {
			t.Errorf("path %q does not contain bundle name", path)
		}
		storePath := env.GetStorePath()
		if !strings.HasPrefix(path, storePath) {
			t.Errorf("bundle path %q does not start with store path %q", path, storePath)
		}
	})

	t.Run("error for unknown bundle", func(t *testing.T) {
		bm := New(MapOfApps{}, MapOfBundles{}, nil)

		_, err := bm.ComputeBundlePath("nonexistent")
		if err == nil {
			t.Error("expected error for unknown bundle")
		}
	})

	t.Run("deterministic path", func(t *testing.T) {
		bundles := MapOfBundles{
			"test-bundle": {
				Version: "1.0",
				Files:   map[string]string{"a.txt": "hello"},
			},
		}
		bm := New(MapOfApps{}, bundles, nil)

		path1, _ := bm.ComputeBundlePath("test-bundle")
		path2, _ := bm.ComputeBundlePath("test-bundle")
		if path1 != path2 {
			t.Error("path is not deterministic")
		}
	})

	t.Run("different content produces different path", func(t *testing.T) {
		bm1 := New(MapOfApps{}, MapOfBundles{
			"test-bundle": {Version: "1.0", Files: map[string]string{"a.txt": "hello"}},
		}, nil)
		bm2 := New(MapOfApps{}, MapOfBundles{
			"test-bundle": {Version: "1.0", Files: map[string]string{"a.txt": "world"}},
		}, nil)

		path1, _ := bm1.ComputeBundlePath("test-bundle")
		path2, _ := bm2.ComputeBundlePath("test-bundle")
		if path1 == path2 {
			t.Error("different content produced same path")
		}
	})
}

func TestGetBundleRoot(t *testing.T) {
	t.Run("error when bundle not installed", func(t *testing.T) {
		bundles := MapOfBundles{
			"test-bundle": {
				Version: "1.0",
				Files:   map[string]string{"a.txt": "hello"},
			},
		}
		bm := New(MapOfApps{}, bundles, nil)

		_, err := bm.GetBundleRoot("test-bundle")
		if err == nil {
			t.Error("expected error for uninstalled bundle")
		}
	})

	t.Run("error for unknown bundle", func(t *testing.T) {
		bm := New(MapOfApps{}, MapOfBundles{}, nil)

		_, err := bm.GetBundleRoot("nonexistent")
		if err == nil {
			t.Error("expected error for unknown bundle")
		}
	})

	t.Run("returns path when bundle directory exists", func(t *testing.T) {
		bundles := MapOfBundles{
			"test-bundle": {
				Version: "1.0",
				Files:   map[string]string{"a.txt": "hello"},
			},
		}
		bm := New(MapOfApps{}, bundles, nil)

		bundlePath, _ := bm.ComputeBundlePath("test-bundle")
		if err := os.MkdirAll(bundlePath, 0755); err != nil {
			t.Fatalf("failed to create bundle dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(filepath.Dir(filepath.Dir(bundlePath))) }()

		root, err := bm.GetBundleRoot("test-bundle")
		if err != nil {
			t.Fatalf("GetBundleRoot() error = %v", err)
		}
		if root != bundlePath {
			t.Errorf("GetBundleRoot() = %q, want %q", root, bundlePath)
		}
	})
}

func TestVerifyFileHash(t *testing.T) {
	testContent := "test file content for hash verification"

	sha256Hash := sha256.Sum256([]byte(testContent))
	sha256Hex := hex.EncodeToString(sha256Hash[:])

	sha512Hash := sha512.Sum512([]byte(testContent))
	sha512Hex := hex.EncodeToString(sha512Hash[:])

	sha384Hash := sha512.Sum384([]byte(testContent))
	sha384Hex := hex.EncodeToString(sha384Hash[:])

	sha1Hash := sha1.Sum([]byte(testContent))
	sha1Hex := hex.EncodeToString(sha1Hash[:])

	md5Hash := md5.Sum([]byte(testContent))
	md5Hex := hex.EncodeToString(md5Hash[:])

	tests := []struct {
		name         string
		hashType     BinHashType
		expectedHash string
	}{
		{
			name:         "SHA256",
			hashType:     BinHashTypeSHA256,
			expectedHash: sha256Hex,
		},
		{
			name:         "SHA512",
			hashType:     BinHashTypeSHA512,
			expectedHash: sha512Hex,
		},
		{
			name:         "SHA384",
			hashType:     BinHashTypeSHA384,
			expectedHash: sha384Hex,
		},
		{
			name:         "SHA1",
			hashType:     BinHashTypeSHA1,
			expectedHash: sha1Hex,
		},
		{
			name:         "MD5",
			hashType:     BinHashTypeMD5,
			expectedHash: md5Hex,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "testfile")

			if err := os.WriteFile(filePath, []byte(testContent), 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			err := verifyFileHash(filePath, tt.expectedHash, tt.hashType)
			if err != nil {
				t.Errorf("verifyFileHash() error = %v", err)
			}
		})
	}

	t.Run("hash mismatch", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")

		if err := os.WriteFile(filePath, []byte(testContent), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
		err := verifyFileHash(filePath, wrongHash, BinHashTypeSHA256)
		if err == nil {
			t.Error("expected hash mismatch error, got nil")
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "nonexistent")

		err := verifyFileHash(filePath, sha256Hex, BinHashTypeSHA256)
		if err == nil {
			t.Error("expected file not found error, got nil")
		}
	})

	t.Run("unsupported hash type", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")

		if err := os.WriteFile(filePath, []byte(testContent), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		err := verifyFileHash(filePath, sha256Hex, BinHashType("invalid"))
		if err == nil {
			t.Error("expected unsupported hash type error, got nil")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "empty")

		if err := os.WriteFile(filePath, []byte(""), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		emptyHash := sha256.Sum256([]byte(""))
		emptyHashHex := hex.EncodeToString(emptyHash[:])

		err := verifyFileHash(filePath, emptyHashHex, BinHashTypeSHA256)
		if err != nil {
			t.Errorf("verifyFileHash() error = %v", err)
		}
	})
}
