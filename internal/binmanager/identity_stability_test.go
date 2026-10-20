package binmanager

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/target"
)

// A pin's spelling must never move the store: the migration from bare hex to
// canonical sha256:<hex> pins rewrites config files, not install directories.
// These vectors pin calculateConfigHash both ways; if either changes, every
// existing store entry misses and re-downloads.

func TestCalculateConfigHashSpellingStability(t *testing.T) {
	canonical := "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	bare := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	binaryPath := "bin/app"

	infoCanonical := BinaryOsArchInfo{URL: "https://example.com/a", Hash: canonical, ContentType: BinContentTypeTarGz, BinaryPath: &binaryPath}
	infoBare := BinaryOsArchInfo{URL: "https://example.com/a", Hash: bare, ContentType: BinContentTypeTarGz, BinaryPath: &binaryPath}
	// An uppercase pin normalized on load must land in the same place too.
	infoUpper := BinaryOsArchInfo{URL: "https://example.com/a", Hash: "sha256:BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD", ContentType: BinContentTypeTarGz, BinaryPath: &binaryPath}

	resolved := makeResolvedTarget("linux", "amd64", target.LibcGlibc)

	gotCanonical := calculateConfigHash(infoCanonical, resolved)
	gotBare := calculateConfigHash(infoBare, resolved)
	gotUpper := calculateConfigHash(infoUpper, resolved)

	if gotCanonical == "" || gotCanonical != gotBare || gotCanonical != gotUpper {
		t.Fatalf("pin spelling moved the store identity: canonical=%s bare=%s upper=%s", gotCanonical, gotBare, gotUpper)
	}
}

// The archive pin folds the same way: an external archive's bundle and
// runtime-app identities survive the canonical migration in place.
func TestHashFilesAndArchivesSpellingStability(t *testing.T) {
	canonical := "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	bare := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	format := BinContentTypeTarGz

	canonicalHash := HashFilesAndArchives(nil, map[string]*ArchiveSpec{
		"a": {URL: "https://example.com/a.tar.gz", Hash: canonical, Format: format},
	})
	bareHash := HashFilesAndArchives(nil, map[string]*ArchiveSpec{
		"a": {URL: "https://example.com/a.tar.gz", Hash: bare, Format: format},
	})

	if canonicalHash == "" || canonicalHash != bareHash {
		t.Fatalf("archive pin spelling moved the identity: canonical=%s bare=%s", canonicalHash, bareHash)
	}
}
