package binmanager

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/target"
	"go.uber.org/zap"
)

// calculateConfigHash calculates XXH3-128 hash of binary configuration using the resolved target.
// The resolved target (not the host target) determines the cache path, ensuring that
// glibc and musl binaries get separate cache entries.
func calculateConfigHash(info BinaryOsArchInfo, resolved target.ResolvedTarget) string {
	binaryPath := ""
	if info.BinaryPath != nil {
		binaryPath = *info.BinaryPath
	}
	extractDir := ""
	if info.ExtractDir {
		extractDir = "extractDir"
	}
	return hashutil.XXH3Multi(
		[]byte(info.URL),
		[]byte(info.Hash),
		[]byte(info.ContentType),
		[]byte(binaryPath),
		[]byte(extractDir),
		[]byte(resolved.Target.OS),
		[]byte(resolved.Target.Arch),
		[]byte(string(resolved.Target.Libc)),
	)
}

// verifyFileHash verifies a downloaded file's integrity using cryptographic hashes.
// Used exclusively for external verification of content downloaded from the internet.
func verifyFileHash(filePath string, expectedHash string, hashType BinHashType) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for verification: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close file during hash verification", zap.Error(err))
		}
	}()

	var actualHash string

	switch hashType {
	case BinHashTypeSHA256:
		h := sha256.New()
		if _, err := io.Copy(h, file); err != nil {
			return fmt.Errorf("failed to calculate sha256: %w", err)
		}
		actualHash = hex.EncodeToString(h.Sum(nil))

	case BinHashTypeSHA512:
		h := sha512.New()
		if _, err := io.Copy(h, file); err != nil {
			return fmt.Errorf("failed to calculate sha512: %w", err)
		}
		actualHash = hex.EncodeToString(h.Sum(nil))

	case BinHashTypeSHA384:
		h := sha512.New384()
		if _, err := io.Copy(h, file); err != nil {
			return fmt.Errorf("failed to calculate sha384: %w", err)
		}
		actualHash = hex.EncodeToString(h.Sum(nil))

	case BinHashTypeSHA1:
		h := sha1.New()
		if _, err := io.Copy(h, file); err != nil {
			return fmt.Errorf("failed to calculate sha1: %w", err)
		}
		actualHash = hex.EncodeToString(h.Sum(nil))

	case BinHashTypeMD5:
		h := md5.New()
		if _, err := io.Copy(h, file); err != nil {
			return fmt.Errorf("failed to calculate md5: %w", err)
		}
		actualHash = hex.EncodeToString(h.Sum(nil))

	default:
		return fmt.Errorf("unsupported hash type: %s", hashType)
	}

	if actualHash != expectedHash {
		return fmt.Errorf("hash mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

// HashFilesAndArchives computes an XXH3-128 hash over files and archives content.
// Returns empty string when both maps are empty.
func HashFilesAndArchives(files map[string]string, archives map[string]*ArchiveSpec) string {
	if len(files) == 0 && len(archives) == 0 {
		return ""
	}

	// Build a single byte slice with all content for hashing.
	var buf []byte

	if len(files) > 0 {
		keys := make([]string, 0, len(files))
		for k := range files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			buf = append(buf, "file:"...)
			buf = append(buf, k...)
			buf = append(buf, 0)
			buf = append(buf, files[k]...)
			buf = append(buf, 0)
		}
	}

	if len(archives) > 0 {
		archKeys := make([]string, 0, len(archives))
		for k := range archives {
			archKeys = append(archKeys, k)
		}
		sort.Strings(archKeys)
		for _, k := range archKeys {
			spec := archives[k]
			if spec == nil {
				continue
			}
			buf = append(buf, "archive:"...)
			buf = append(buf, k...)
			buf = append(buf, 0)
			if spec.IsInline() {
				buf = append(buf, spec.Inline...)
			} else {
				buf = append(buf, spec.URL...)
				buf = append(buf, 0)
				buf = append(buf, spec.Hash...)
				buf = append(buf, 0)
				buf = append(buf, spec.Format...)
			}
			buf = append(buf, 0)
		}
	}

	return hashutil.XXH3Hex(buf)
}

func calculateBundleHash(name, version string, files map[string]string, archives map[string]*ArchiveSpec) string {
	return hashutil.XXH3Multi(
		[]byte(name),
		[]byte(version),
		[]byte(HashFilesAndArchives(files, archives)),
	)
}

// ComputeBundlePath returns the install directory path for a bundle without checking existence.
func (bm *BinManager) ComputeBundlePath(name string) (string, error) {
	bundle, ok := bm.mapOfBundles[name]
	if !ok {
		return "", fmt.Errorf("bundle %q not found", name)
	}

	hash := calculateBundleHash(name, bundle.Version, bundle.Files, bundle.Archives)
	return filepath.Join(env.GetStorePath(), ".bundles", name, hash), nil
}

// GetBundleRoot returns the install directory for a bundle, verifying it exists.
func (bm *BinManager) GetBundleRoot(name string) (string, error) {
	bundlePath, err := bm.ComputeBundlePath(name)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(bundlePath); err != nil {
		return "", fmt.Errorf("bundle %q is not installed (path %s does not exist)", name, bundlePath)
	}

	return bundlePath, nil
}
