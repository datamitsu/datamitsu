package detector

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func makeAsset(name string) github.Asset {
	return github.Asset{Name: name, BrowserDownloadURL: "https://example.com/" + name}
}

func TestScoreAsset_OSMatching(t *testing.T) {
	tests := []struct {
		name      string
		asset     string
		osType    syslist.OsType
		archType  syslist.ArchType
		wantOS    bool
		wantTotal int
	}{
		{"linux matches linux", "tool-linux-amd64.tar.gz", syslist.OsTypeLinux, syslist.ArchTypeAmd64, true, scoreOS + scoreArch + scoreLibcNeutral + scoreArchivePrefer},
		{"darwin matches darwin", "tool-darwin-arm64.tar.gz", syslist.OsTypeDarwin, syslist.ArchTypeArm64, true, scoreOS + scoreArch + scoreLibcNeutral + scoreArchivePrefer},
		{"wrong OS scores 0", "tool-darwin-amd64.tar.gz", syslist.OsTypeLinux, syslist.ArchTypeAmd64, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScoreAsset(makeAsset(tt.asset), tt.osType, tt.archType, "")
			if s.OSMatch != tt.wantOS {
				t.Errorf("OSMatch = %v, want %v", s.OSMatch, tt.wantOS)
			}
			if s.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", s.Total, tt.wantTotal)
			}
		})
	}
}

func TestScoreAsset_ArchMatching(t *testing.T) {
	tests := []struct {
		name      string
		asset     string
		archType  syslist.ArchType
		wantArch  bool
		wantTotal int
	}{
		{"amd64 matches amd64", "tool-linux-amd64.tar.gz", syslist.ArchTypeAmd64, true, scoreOS + scoreArch + scoreLibcNeutral + scoreArchivePrefer},
		{"arm64 matches arm64", "tool-linux-arm64.tar.gz", syslist.ArchTypeArm64, true, scoreOS + scoreArch + scoreLibcNeutral + scoreArchivePrefer},
		{"wrong arch low score", "tool-linux-arm64.tar.gz", syslist.ArchTypeAmd64, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScoreAsset(makeAsset(tt.asset), syslist.OsTypeLinux, tt.archType, "")
			if s.ArchMatch != tt.wantArch {
				t.Errorf("ArchMatch = %v, want %v", s.ArchMatch, tt.wantArch)
			}
			if s.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", s.Total, tt.wantTotal)
			}
		})
	}
}

func TestScoreAsset_LibcMatching(t *testing.T) {
	tests := []struct {
		name      string
		asset     string
		libcType  string
		wantLibc  int
		wantTotal int
	}{
		{
			"musl asset matches musl request",
			"tool-linux-musl-amd64.tar.gz", "musl",
			2, scoreOS + scoreArch + scoreLibcExact + scoreArchivePrefer,
		},
		{
			"glibc asset matches glibc request",
			"tool-linux-gnu-amd64.tar.gz", "glibc",
			2, scoreOS + scoreArch + scoreLibcExact + scoreArchivePrefer,
		},
		{
			"no libc indicator neutral for musl request",
			"tool-linux-amd64.tar.gz", "musl",
			1, scoreOS + scoreArch + scoreLibcNeutral + scoreArchivePrefer,
		},
		{
			"musl asset mismatch for glibc request",
			"tool-linux-musl-amd64.tar.gz", "glibc",
			0, scoreOS + scoreArch + scoreLibcMismatch + scoreArchivePrefer,
		},
		{
			"no libc indicator neutral for empty request",
			"tool-linux-amd64.tar.gz", "",
			1, scoreOS + scoreArch + scoreLibcNeutral + scoreArchivePrefer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScoreAsset(makeAsset(tt.asset), syslist.OsTypeLinux, syslist.ArchTypeAmd64, tt.libcType)
			if s.LibcMatch != tt.wantLibc {
				t.Errorf("LibcMatch = %d, want %d", s.LibcMatch, tt.wantLibc)
			}
			if s.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", s.Total, tt.wantTotal)
			}
		})
	}
}

func TestScoreAsset_PriorityPattern(t *testing.T) {
	s := ScoreAsset(makeAsset("tool-linux-amd64.AppImage"), syslist.OsTypeLinux, syslist.ArchTypeAmd64, "")
	if !s.HasPriority {
		t.Error("expected HasPriority=true for .AppImage")
	}
	if s.Total != scoreOS+scoreArch+scoreLibcNeutral+scorePriority {
		t.Errorf("Total = %d, want %d", s.Total, scoreOS+scoreArch+scoreLibcNeutral+scorePriority)
	}
}

func TestScoreAsset_ArchiveBonus(t *testing.T) {
	archiveScore := ScoreAsset(makeAsset("tool-linux-amd64.tar.gz"), syslist.OsTypeLinux, syslist.ArchTypeAmd64, "")
	binaryScore := ScoreAsset(makeAsset("tool-linux-amd64"), syslist.OsTypeLinux, syslist.ArchTypeAmd64, "")

	if archiveScore.ArchiveBonus != scoreArchivePrefer {
		t.Errorf("archive bonus = %d, want %d", archiveScore.ArchiveBonus, scoreArchivePrefer)
	}
	if binaryScore.ArchiveBonus != 0 {
		t.Errorf("binary bonus = %d, want 0", binaryScore.ArchiveBonus)
	}
	if archiveScore.Total <= binaryScore.Total {
		t.Errorf("archive score %d should be greater than binary score %d", archiveScore.Total, binaryScore.Total)
	}
}

func TestSelectBestAsset_DeterministicTiebreaking(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tool-linux-amd64-b.tar.gz"),
		makeAsset("tool-linux-amd64-a.tar.gz"),
	}

	best := selectBestAsset(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "")
	if best == nil {
		t.Fatal("expected a result")
	}
	if best.Asset.Name != "tool-linux-amd64-a.tar.gz" {
		t.Errorf("expected alphabetically first asset, got %q", best.Asset.Name)
	}
}

func TestSelectBestAsset_MuslPreferred(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tool-linux-gnu-amd64.tar.gz"),
		makeAsset("tool-linux-musl-amd64.tar.gz"),
	}

	best := selectBestAsset(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl")
	if best == nil {
		t.Fatal("expected a result")
	}
	if best.Asset.Name != "tool-linux-musl-amd64.tar.gz" {
		t.Errorf("expected musl asset, got %q", best.Asset.Name)
	}
}

func TestSelectBestAsset_GlibcPreferred(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tool-linux-musl-amd64.tar.gz"),
		makeAsset("tool-linux-gnu-amd64.tar.gz"),
	}

	best := selectBestAsset(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
	if best == nil {
		t.Fatal("expected a result")
	}
	if best.Asset.Name != "tool-linux-gnu-amd64.tar.gz" {
		t.Errorf("expected glibc asset, got %q", best.Asset.Name)
	}
}

func TestSelectBestAsset_NoMatch(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tool-darwin-arm64.tar.gz"),
		makeAsset("tool-windows-amd64.zip"),
	}

	best := selectBestAsset(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "")
	if best != nil {
		t.Errorf("expected nil, got %v", best)
	}
}

func TestSelectBestAsset_ArchiveBeatsPlainBinary(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tool-linux-amd64"),
		makeAsset("tool-linux-amd64.tar.gz"),
	}

	best := selectBestAsset(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "")
	if best == nil {
		t.Fatal("expected a result")
	}
	if best.Asset.Name != "tool-linux-amd64.tar.gz" {
		t.Errorf("expected archive, got %q", best.Asset.Name)
	}
}

func TestScoreAsset_FNMMacOSImplicitArm64(t *testing.T) {
	implicitTotal := scoreOS + scoreArch + scoreLibcNeutral + scoreArchivePrefer
	tests := []struct {
		name        string
		asset       string
		osType      syslist.OsType
		archType    syslist.ArchType
		wantOSMatch bool
		wantArch    bool
		wantTotal   int
	}{
		{
			"fnm-macos.zip matches darwin/arm64 implicitly",
			"fnm-macos.zip",
			syslist.OsTypeDarwin, syslist.ArchTypeArm64,
			true, true, implicitTotal,
		},
		{
			"fnm-macos.zip still matches darwin/amd64 implicitly (regression)",
			"fnm-macos.zip",
			syslist.OsTypeDarwin, syslist.ArchTypeAmd64,
			true, true, implicitTotal,
		},
		{
			"linux asset without arch indicator does NOT match linux/arm64",
			"tool-linux.tar.gz",
			syslist.OsTypeLinux, syslist.ArchTypeArm64,
			true, false, 0,
		},
		{
			"windows asset without arch indicator does NOT match windows/arm64",
			"tool-windows.zip",
			syslist.OsTypeWindows, syslist.ArchTypeArm64,
			true, false, 0,
		},
		{
			"fnm-macos.zip does NOT match windows/arm64",
			"fnm-macos.zip",
			syslist.OsTypeWindows, syslist.ArchTypeArm64,
			false, false, 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScoreAsset(makeAsset(tt.asset), tt.osType, tt.archType, "")
			if s.OSMatch != tt.wantOSMatch {
				t.Errorf("OSMatch = %v, want %v", s.OSMatch, tt.wantOSMatch)
			}
			if s.ArchMatch != tt.wantArch {
				t.Errorf("ArchMatch = %v, want %v", s.ArchMatch, tt.wantArch)
			}
			if s.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", s.Total, tt.wantTotal)
			}
		})
	}
}

func TestScoreAsset_IsExplicit(t *testing.T) {
	tests := []struct {
		name        string
		asset       string
		osType      syslist.OsType
		archType    syslist.ArchType
		wantExplicit bool
	}{
		{
			"explicit darwin/arm64 match",
			"tool-darwin-arm64.tar.gz",
			syslist.OsTypeDarwin, syslist.ArchTypeArm64,
			true,
		},
		{
			"explicit linux/amd64 match",
			"tool-linux-amd64.tar.gz",
			syslist.OsTypeLinux, syslist.ArchTypeAmd64,
			true,
		},
		{
			"implicit darwin/arm64 (no arch indicator)",
			"fnm-macos.zip",
			syslist.OsTypeDarwin, syslist.ArchTypeArm64,
			false,
		},
		{
			"implicit darwin/amd64 (no arch indicator)",
			"fnm-macos.zip",
			syslist.OsTypeDarwin, syslist.ArchTypeAmd64,
			false,
		},
		{
			"implicit linux/amd64 (arch only, no OS)",
			"tool-amd64.tar.gz",
			syslist.OsTypeLinux, syslist.ArchTypeAmd64,
			false,
		},
		{
			"no match leaves IsExplicit false",
			"tool-darwin-arm64.tar.gz",
			syslist.OsTypeLinux, syslist.ArchTypeAmd64,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScoreAsset(makeAsset(tt.asset), tt.osType, tt.archType, "")
			if s.IsExplicit != tt.wantExplicit {
				t.Errorf("IsExplicit = %v, want %v", s.IsExplicit, tt.wantExplicit)
			}
		})
	}
}

func TestSelectBestAsset_ExplicitArm64PreferredOverImplicit(t *testing.T) {
	assets := []github.Asset{
		makeAsset("fnm-macos.zip"),
		makeAsset("tool-darwin-arm64.tar.gz"),
	}

	best := selectBestAsset(assets, syslist.OsTypeDarwin, syslist.ArchTypeArm64, "")
	if best == nil {
		t.Fatal("expected a result")
	}
	if best.Asset.Name != "tool-darwin-arm64.tar.gz" {
		t.Errorf("expected explicit arm64 asset to win, got %q", best.Asset.Name)
	}
}

func TestDetectBinary_FiltersVsix(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tombi-vscode-0.1.0-linux-x64.vsix"),
		makeAsset("tombi-vscode-0.1.0-linux-arm64.vsix"),
	}

	_, err := DetectBinary(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
	if err == nil {
		t.Fatal("expected error when only .vsix assets are present, got nil")
	}
	if !strings.Contains(err.Error(), "non-executable") {
		t.Errorf("expected error to mention non-executable, got: %v", err)
	}
}

func TestDetectBinary_PrefersArchiveOverVsix(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tombi-cli-0.1.0-x86_64-unknown-linux-musl.tar.gz"),
		makeAsset("tombi-vscode-0.1.0-linux-x64.vsix"),
	}

	result, err := DetectBinary(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "tombi-cli-0.1.0-x86_64-unknown-linux-musl.tar.gz" {
		t.Errorf("expected tar.gz binary, got %q", result.Name)
	}
}

func TestDetectBinary_ScoringBased(t *testing.T) {
	tests := []struct {
		name      string
		assets    []github.Asset
		osType    syslist.OsType
		archType  syslist.ArchType
		libcType  string
		wantAsset string
		wantErr   bool
	}{
		{
			"selects musl variant for musl request",
			[]github.Asset{
				makeAsset("tool-linux-amd64.tar.gz"),
				makeAsset("tool-linux-musl-amd64.tar.gz"),
				makeAsset("tool-darwin-arm64.tar.gz"),
			},
			syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl",
			"tool-linux-musl-amd64.tar.gz", false,
		},
		{
			"selects generic when no libc requested",
			[]github.Asset{
				makeAsset("tool-linux-musl-amd64.tar.gz"),
				makeAsset("tool-linux-amd64.tar.gz"),
			},
			syslist.OsTypeLinux, syslist.ArchTypeAmd64, "",
			"tool-linux-amd64.tar.gz", false,
		},
		{
			"no assets error",
			nil,
			syslist.OsTypeLinux, syslist.ArchTypeAmd64, "",
			"", true,
		},
		{
			"no match error",
			[]github.Asset{makeAsset("tool-darwin-arm64.tar.gz")},
			syslist.OsTypeLinux, syslist.ArchTypeAmd64, "",
			"", true,
		},
		{
			"filters checksum files",
			[]github.Asset{
				makeAsset("checksums.sha256"),
				makeAsset("tool-linux-amd64.tar.gz"),
			},
			syslist.OsTypeLinux, syslist.ArchTypeAmd64, "",
			"tool-linux-amd64.tar.gz", false,
		},
		{
			"filters non-executable package files",
			[]github.Asset{
				makeAsset("tool-linux-amd64.deb"),
				makeAsset("tool-linux-amd64.tar.gz"),
			},
			syslist.OsTypeLinux, syslist.ArchTypeAmd64, "",
			"tool-linux-amd64.tar.gz", false,
		},
		{
			"filters macOS pkg installer",
			[]github.Asset{
				makeAsset("tool-darwin-arm64.pkg"),
				makeAsset("tool-darwin-arm64.tar.gz"),
			},
			syslist.OsTypeDarwin, syslist.ArchTypeArm64, "",
			"tool-darwin-arm64.tar.gz", false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := DetectBinary(tt.assets, tt.osType, tt.archType, tt.libcType)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Name != tt.wantAsset {
				t.Errorf("got %q, want %q", result.Name, tt.wantAsset)
			}
		})
	}
}
