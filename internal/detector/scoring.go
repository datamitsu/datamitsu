package detector

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"sort"
)

// AssetScore holds the breakdown of an asset's score
type AssetScore struct {
	Asset        github.Asset
	Total        int
	OSMatch      bool
	ArchMatch    bool
	LibcMatch    int // 0=mismatch, 1=neutral (no indicator), 2=exact
	HasPriority  bool
	ArchiveBonus int
}

const (
	scoreOS            = 1000
	scoreArch          = 100
	scoreLibcExact     = 10
	scoreLibcNeutral   = 5
	scoreLibcMismatch  = 1
	scorePriority      = 50
	scoreArchivePrefer = 2
)

// ScoreAsset computes a match score for a GitHub release asset against the
// requested OS, architecture, and libc type. Higher scores indicate better matches.
func ScoreAsset(asset github.Asset, osType syslist.OsType, archType syslist.ArchType, libcType string) AssetScore {
	s := AssetScore{Asset: asset}

	osMatch := MatchOS(asset.Name, osType)
	archMatch := MatchArch(asset.Name, archType)

	// Handle implicit matches for binaries that only specify one dimension
	hasOSIndicator := HasAnyOSIndicator(asset.Name)
	hasArchIndicator := HasAnyArchIndicator(asset.Name)

	// Implicit amd64: OS matches, no arch indicators, requesting amd64
	if osMatch && !hasArchIndicator && archType == syslist.ArchTypeAmd64 {
		s.OSMatch = true
		s.ArchMatch = true
	} else if archMatch && !hasOSIndicator && osType == syslist.OsTypeLinux {
		// Implicit Linux: Arch matches, no OS indicators, requesting Linux
		s.OSMatch = true
		s.ArchMatch = true
	} else {
		// Standard explicit matching
		s.OSMatch = osMatch
		s.ArchMatch = archMatch
	}

	if !s.OSMatch || !s.ArchMatch {
		return s
	}

	s.Total += scoreOS + scoreArch

	detectedLibc := DetectLibcFromFilename(asset.Name)
	switch {
	case detectedLibc == "" && libcType == "":
		s.LibcMatch = 1
		s.Total += scoreLibcNeutral
	case detectedLibc == "" && libcType != "":
		s.LibcMatch = 1
		s.Total += scoreLibcNeutral
	case detectedLibc != "" && libcType == "":
		s.LibcMatch = 1
		s.Total += scoreLibcNeutral
	case detectedLibc == libcType:
		s.LibcMatch = 2
		s.Total += scoreLibcExact
	default:
		s.LibcMatch = 0
		s.Total += scoreLibcMismatch
	}

	if HasPriorityPattern(asset.Name, osType) {
		s.HasPriority = true
		s.Total += scorePriority
	}

	contentType := DetectContentType(asset.Name)
	if contentType != binmanager.BinContentTypeBinary {
		s.ArchiveBonus = scoreArchivePrefer
		s.Total += scoreArchivePrefer
	}

	return s
}

// selectBestAsset scores all assets and returns the highest-scoring one.
// Ties are broken by asset name (alphabetical, ascending) for determinism.
func selectBestAsset(assets []github.Asset, osType syslist.OsType, archType syslist.ArchType, libcType string) *AssetScore {
	if len(assets) == 0 {
		return nil
	}

	scores := make([]AssetScore, 0, len(assets))
	for _, asset := range assets {
		s := ScoreAsset(asset, osType, archType, libcType)
		if s.Total > 0 && s.OSMatch && s.ArchMatch {
			scores = append(scores, s)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Total != scores[j].Total {
			return scores[i].Total > scores[j].Total
		}
		return scores[i].Asset.Name < scores[j].Asset.Name
	})

	return &scores[0]
}
