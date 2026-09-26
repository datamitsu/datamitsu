package detector

import (
	"sort"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// AssetScore holds the breakdown of an asset's score
type AssetScore struct {
	Asset        github.Asset
	Total        int
	OSMatch      bool
	ArchMatch    bool
	IsExplicit   bool // true when both OS and arch were matched explicitly (no implicit rule)
	LibcMatch    int  // 0=mismatch, 1=neutral (no indicator), 2=exact
	NameMatch    bool // the asset's name carries the app's name
	HasPriority  bool
	ArchiveBonus int
}

// Points per criterion, each outranking every criterion below it combined:
// OS and architecture, then libc — an asset that names the requested libc
// beats one that names none, which beats one that names another — then the
// app's own name, then format. A release can hold several tools of one
// project (harper-cli, harper-ls and the Harper desktop app share one), so
// the asset named after the app outranks a sibling's, whatever its format;
// but a build that names the right libc still outranks one that merely
// carries the name, since a glibc binary recorded for musl fails at run time.
const (
	scoreOS            = 10000
	scoreArch          = 1000
	scoreLibcExact     = 300
	scoreLibcNeutral   = 200
	scoreLibcMismatch  = 100
	scoreNameMatch     = 60
	scorePriority      = 20
	scoreArchivePrefer = 2
)

// ScoreAsset computes a match score for a GitHub release asset against the
// requested OS, architecture, and libc type, without an app name to prefer.
// Higher scores indicate better matches.
func ScoreAsset(asset github.Asset, osType syslist.OsType, archType syslist.ArchType, libcType string) AssetScore {
	return scoreAssetFor("", asset, osType, archType, libcType)
}

// scoreAssetFor scores an asset for appName: one whose name carries the app's
// name earns scoreNameMatch on top of the platform criteria.
func scoreAssetFor(appName string, asset github.Asset, osType syslist.OsType, archType syslist.ArchType, libcType string) AssetScore {
	s := AssetScore{Asset: asset}

	osMatch := MatchOS(asset.Name, osType)
	archMatch := MatchArch(asset.Name, archType)

	// Handle implicit matches for binaries that only specify one dimension
	hasOSIndicator := HasAnyOSIndicator(asset.Name)
	hasArchIndicator := HasAnyArchIndicator(asset.Name)

	switch {
	// Implicit amd64: OS matches, no arch indicators, requesting amd64
	case osMatch && !hasArchIndicator && archType == syslist.ArchTypeAmd64:
		s.OSMatch = true
		s.ArchMatch = true
	case osMatch && !hasArchIndicator && osType == syslist.OsTypeDarwin && archType == syslist.ArchTypeArm64:
		// Implicit darwin/arm64: OS matches darwin, no arch indicators, requesting arm64.
		// Many macOS-only assets (e.g. a *-macos.zip) ship universal binaries that
		// run on both Intel and Apple Silicon without declaring arch in the filename.
		s.OSMatch = true
		s.ArchMatch = true
	case archMatch && !hasOSIndicator && osType == syslist.OsTypeLinux:
		// Implicit Linux: Arch matches, no OS indicators, requesting Linux
		s.OSMatch = true
		s.ArchMatch = true
	default:
		// Standard explicit matching
		s.OSMatch = osMatch
		s.ArchMatch = archMatch
		if osMatch && archMatch {
			s.IsExplicit = true
		}
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

	if nameMatches(appName, asset.Name) {
		s.NameMatch = true
		s.Total += scoreNameMatch
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

// rankAssets scores every asset and returns the matching ones sorted best-first.
// Assets that fail to match the requested OS/arch (score 0) are excluded. The
// ordering is deterministic: score descending, then explicit matches before
// implicit ones, then asset name ascending. Callers that only need the winner
// use the first element; callers that want fallbacks (e.g. a raw binary behind
// a preferred archive) walk the whole slice.
func rankAssets(appName string, assets []github.Asset, osType syslist.OsType, archType syslist.ArchType, libcType string) []AssetScore {
	scores := make([]AssetScore, 0, len(assets))
	for _, asset := range assets {
		s := scoreAssetFor(appName, asset, osType, archType, libcType)
		if s.Total > 0 && s.OSMatch && s.ArchMatch {
			scores = append(scores, s)
		}
	}

	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Total != scores[j].Total {
			return scores[i].Total > scores[j].Total
		}
		if scores[i].IsExplicit != scores[j].IsExplicit {
			return scores[i].IsExplicit
		}
		return scores[i].Asset.Name < scores[j].Asset.Name
	})

	return scores
}

// selectBestAsset scores all assets, without an app name to prefer, and
// returns the highest-scoring one. Ties are broken by asset name
// (alphabetical, ascending) for determinism.
func selectBestAsset(assets []github.Asset, osType syslist.OsType, archType syslist.ArchType, libcType string) *AssetScore {
	ranked := rankAssets("", assets, osType, archType, libcType)
	if len(ranked) == 0 {
		return nil
	}
	return &ranked[0]
}
