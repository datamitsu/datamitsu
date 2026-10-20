package binmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/target"
)

// calculateConfigHash calculates XXH3-128 hash of binary configuration using the resolved target.
// The resolved target (not the host target) determines the cache path, ensuring that
// glibc and musl binaries get separate cache entries. The pin folds by its hex
// value alone, so a pin's spelling (bare or canonical) never moves the store.
func calculateConfigHash(info BinaryOsArchInfo, resolved target.ResolvedTarget) string {
	binaryPath := ""
	if info.BinaryPath != nil {
		binaryPath = *info.BinaryPath
	}
	extractDir := ""
	if info.ExtractDir {
		extractDir = "extractDir"
	}
	pin := info.Hash
	if d, err := digest.ParseSHA256Loose(info.Hash); err == nil {
		pin = d.Hex()
	}
	return digest.XXH3Multi(
		[]byte(info.URL),
		[]byte(pin),
		[]byte(info.ContentType),
		[]byte(binaryPath),
		[]byte(extractDir),
		[]byte(resolved.Target.OS),
		[]byte(resolved.Target.Arch),
		[]byte(string(resolved.Target.Libc)),
	).Hex()
}

// verifyFileHash verifies a downloaded file's integrity against its SHA-256
// pin. Used exclusively for external verification of content downloaded from
// the internet; the security policy admits no other algorithm.
func verifyFileHash(filePath string, expectedHash string) error {
	d, err := digest.ParseSHA256Loose(expectedHash)
	if err != nil {
		return fmt.Errorf("invalid pinned hash: %w", err)
	}
	return d.VerifyFile(filePath)
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
				// The pin folds by its hex value, so a pin's spelling (bare or
				// canonical) never moves the bundle or app identity.
				pin := spec.Hash
				if d, err := digest.ParseSHA256Loose(spec.Hash); err == nil {
					pin = d.Hex()
				}
				buf = append(buf, spec.URL...)
				buf = append(buf, 0)
				buf = append(buf, pin...)
				buf = append(buf, 0)
				buf = append(buf, spec.Format...)
			}
			buf = append(buf, 0)
		}
	}

	return digest.XXH3Of(buf).Hex()
}

func calculateBundleHash(name, version string, files map[string]string, archives map[string]*ArchiveSpec) string {
	return digest.XXH3Multi(
		[]byte(name),
		[]byte(version),
		[]byte(HashFilesAndArchives(files, archives)),
	).Hex()
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
