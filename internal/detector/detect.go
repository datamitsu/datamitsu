package detector

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"fmt"
	"strings"
)

// DetectBinary finds the best matching asset for given OS, architecture, and libc type.
// Uses scoring-based selection: each asset is scored by OS, Arch, Libc match quality,
// archive format preference, and priority patterns. The highest-scoring asset wins.
// Ties are broken alphabetically by asset name for determinism.
func DetectBinary(assets []github.Asset, osType syslist.OsType, archType syslist.ArchType, libcType string) (*github.Asset, error) {
	if len(assets) == 0 {
		return nil, fmt.Errorf("no assets available")
	}

	validAssets := filterValidAssets(assets)
	if len(validAssets) == 0 {
		return nil, fmt.Errorf("no valid assets found (all were checksum files)")
	}

	best := selectBestAsset(validAssets, osType, archType, libcType)
	if best == nil {
		return nil, fmt.Errorf("no matching binary found for %s/%s", osType, archType)
	}

	return &best.Asset, nil
}

// filterValidAssets removes checksum and invalid files
func filterValidAssets(assets []github.Asset) []github.Asset {
	var valid []github.Asset
	for _, asset := range assets {
		if !IsChecksumFile(asset.Name) {
			valid = append(valid, asset)
		}
	}
	return valid
}

// HasAnyOSIndicator checks if the filename contains ANY OS indicator
func HasAnyOSIndicator(filename string) bool {
	for osType := range OSPatterns {
		if MatchOS(filename, osType) {
			return true
		}
	}
	return false
}

// HasAnyArchIndicator checks if the filename contains ANY architecture indicator
func HasAnyArchIndicator(filename string) bool {
	for archType := range ArchPatterns {
		if MatchArch(filename, archType) {
			return true
		}
	}
	return false
}

// DetectContentType determines the content type from filename
func DetectContentType(filename string) binmanager.BinContentType {
	lowerName := strings.ToLower(filename)

	if strings.HasSuffix(lowerName, ".tar.gz") || strings.HasSuffix(lowerName, ".tgz") {
		return binmanager.BinContentTypeTarGz
	}
	if strings.HasSuffix(lowerName, ".tar.bz2") || strings.HasSuffix(lowerName, ".tbz") {
		return binmanager.BinContentTypeTarBz2
	}
	if strings.HasSuffix(lowerName, ".tar.xz") || strings.HasSuffix(lowerName, ".txz") {
		return binmanager.BinContentTypeTarXz
	}
	if strings.HasSuffix(lowerName, ".tar.zst") {
		return binmanager.BinContentTypeTarZst
	}

	if strings.HasSuffix(lowerName, ".tar") {
		return binmanager.BinContentTypeTar
	}

	if strings.HasSuffix(lowerName, ".zip") {
		return binmanager.BinContentTypeZip
	}

	if strings.HasSuffix(lowerName, ".gz") && !strings.Contains(lowerName, ".tar") {
		return binmanager.BinContentTypeGz
	}
	if strings.HasSuffix(lowerName, ".bz2") && !strings.Contains(lowerName, ".tar") {
		return binmanager.BinContentTypeBz2
	}
	if strings.HasSuffix(lowerName, ".xz") && !strings.Contains(lowerName, ".tar") {
		return binmanager.BinContentTypeXz
	}
	if strings.HasSuffix(lowerName, ".zst") && !strings.Contains(lowerName, ".tar") {
		return binmanager.BinContentTypeZst
	}

	return binmanager.BinContentTypeBinary
}
