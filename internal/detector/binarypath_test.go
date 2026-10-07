package detector

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

func TestIsDigit(t *testing.T) {
	for c := byte('0'); c <= '9'; c++ {
		if !isDigit(c) {
			t.Errorf("isDigit(%q) = false, want true", c)
		}
	}
	for _, c := range []byte{'a', 'z', '/', '.', '-', ' ', 'A'} {
		if isDigit(c) {
			t.Errorf("isDigit(%q) = true, want false", c)
		}
	}
}

func TestIsValidVersion(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1.2", true},
		{"2.7.2", true},
		{"0.56.4", true},
		{"1.2.3", true},
		{"123", false},     // no dot
		{"1", false},       // no dot
		{"1.", false},      // empty trailing part
		{".1", false},      // empty leading part
		{"1.2.3.4", true},  // many parts OK
		{"1.a", false},     // non-numeric part
		{"v1.2", false},    // 'v' prefix is not numeric here
		{"1..2", false},    // empty middle part
		{"", false},        // empty
		{"abc.def", false}, // non-numeric
	}
	for _, tt := range tests {
		if got := isValidVersion(tt.in); got != tt.want {
			t.Errorf("isValidVersion(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestExtractVersionFromString(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"v1.2.3", "v1.2.3"},
		{"1.2.3", "1.2.3"},
		{"tool-v0.56.4-linux", "v0.56.4"},
		{"tool_2.7.2_amd64", "2.7.2"},
		{"app-1.2.3-beta", "1.2.3"},
		{"release", ""},
		{"vendor", ""}, // v not followed by digit
		{"v1", ""},     // not a valid version (no dot)
		{"linux", ""},  // no version
		{"v.1.2", ""},  // v not followed by digit
		{"123456", ""}, // digits but no dot
	}
	for _, tt := range tests {
		if got := extractVersionFromString(tt.in); got != tt.want {
			t.Errorf("extractVersionFromString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"tool-1.2.3.tar.gz", "1.2.3"},
		{"tool-v0.56.4.tar.xz", "v0.56.4"},
		{"tool-2.7.2.zip", "2.7.2"},
		{"tool-1.0.0.tgz", "1.0.0"},
		{"tool-1.0.0.txz", "1.0.0"},
		{"tool-1.0.0.tar.bz2", "1.0.0"},
		{"tool-1.0.0.tar.zst", "1.0.0"},
		{"Tool-1.0.0.ZIP", "1.0.0"},
		{"binary", ""},
		{"tool-1.2", "1.2"},
	}
	for _, tt := range tests {
		if got := extractVersion(tt.in); got != tt.want {
			t.Errorf("extractVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractVersionFromPath(t *testing.T) {
	tests := []struct {
		path        string
		wantVersion string
		wantPart    string
	}{
		{"app-1.2.3/bin/app", "1.2.3", "app-1.2.3"},
		{"bin/app", "", ""},
		{"app-v0.5.0/app", "v0.5.0", "app-v0.5.0"},
		{"app", "", ""},
	}
	for _, tt := range tests {
		gotV, gotP := extractVersionFromPath(tt.path)
		if gotV != tt.wantVersion || gotP != tt.wantPart {
			t.Errorf("extractVersionFromPath(%q) = (%q, %q), want (%q, %q)", tt.path, gotV, gotP, tt.wantVersion, tt.wantPart)
		}
	}
}

func TestArchiveStem(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"tool-1.0.0.tar.gz", "tool-1.0.0"},
		{"tool-1.0.0.tar.bz2", "tool-1.0.0"},
		{"tool-1.0.0.tar.zst", "tool-1.0.0"},
		{"tool-1.0.0.tgz", "tool-1.0.0"},
		{"tool-1.0.0.tar", "tool-1.0.0"},
		{"Tool-1.0.0.ZIP", "Tool-1.0.0"},
		{"tool-linux.exe", "tool-linux.exe"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := archiveStem(tt.in); got != tt.want {
			t.Errorf("archiveStem(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDerivePath(t *testing.T) {
	tests := []struct {
		name        string
		oldAsset    string
		oldPath     string
		newFilename string
		want        string
	}{
		{
			name:        "component named after the asset takes the new name",
			oldAsset:    "tombi-cli-1.5.0-x86_64-apple-darwin.tar.gz",
			oldPath:     "tombi-cli-1.5.0-x86_64-apple-darwin/tombi",
			newFilename: "tombi-cli-1.5.5-x86_64-apple-darwin.tar.gz",
			want:        "tombi-cli-1.5.5-x86_64-apple-darwin/tombi",
		},
		{
			name:        "the stem carries a renamed architecture too",
			oldAsset:    "app-1.0-x86_64-linux.tar.gz",
			oldPath:     "app-1.0-x86_64-linux/bin/app",
			newFilename: "app-1.1-aarch64-linux.tar.gz",
			want:        "app-1.1-aarch64-linux/bin/app",
		},
		{
			name:        "dot-prefixed path keeps its prefix",
			oldAsset:    "yq_linux_amd64.tar.gz",
			oldPath:     "./yq_linux_amd64",
			newFilename: "yq_linux_arm64.tar.gz",
			want:        "./yq_linux_arm64",
		},
		{
			name:        "version substituted when the stem is not a component",
			oldAsset:    "app-1.0.0-linux-amd64.tar.gz",
			oldPath:     "app-1.0.0/bin/app",
			newFilename: "app-2.0.0-linux-amd64.tar.gz",
			want:        "app-2.0.0/bin/app",
		},
		{
			name:        "unknown asset name falls back to the version",
			oldAsset:    "",
			oldPath:     "app-1.0.0/bin/app",
			newFilename: "app-2.0.0.tar.gz",
			want:        "app-2.0.0/bin/app",
		},
		{
			name:        "same version keeps the path",
			oldAsset:    "",
			oldPath:     "app-1.2.3/app",
			newFilename: "app-1.2.3.tar.gz",
			want:        "app-1.2.3/app",
		},
		{
			name:        "no version in the path keeps it",
			oldAsset:    "protoc-35.0-linux-x86_64.zip",
			oldPath:     "bin/protoc",
			newFilename: "protoc-36.2-linux-x86_64.zip",
			want:        "bin/protoc",
		},
		{
			name:        "no version in the new asset keeps the path",
			oldAsset:    "buf-Linux-x86_64.tar.gz",
			oldPath:     "buf/bin/buf",
			newFilename: "buf-linux-amd64.tar.gz",
			want:        "buf/bin/buf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := derivePath(historicalEntry{asset: tt.oldAsset, path: tt.oldPath}, tt.newFilename)
			if got != tt.want {
				t.Errorf("derivePath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPathContradictsAsset(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		arch     syslist.ArchType
		libc     string
		filename string
		want     bool
	}{
		{"own arch", "app-1.0-x86_64-linux/app", syslist.ArchTypeAmd64, "glibc", "app-1.0-x86_64-linux.tar.gz", false},
		{"other arch", "app-1.0-aarch64-apple-darwin/app", syslist.ArchTypeAmd64, "unknown", "app-1.0-x86_64-apple-darwin.tar.gz", true},
		{"other libc than the asset", "app-1.0-x86_64-linux-musl/app", syslist.ArchTypeAmd64, "musl", "app-1.0-x86_64-linux-gnu.tar.gz", true},
		{"other libc than requested, neutral asset", "app-1.0-x86_64-linux-musl/app", syslist.ArchTypeAmd64, "glibc", "app-1.0-x86_64-linux.tar.gz", true},
		{"libc in path only, unknown requested", "app-1.0-x86_64-linux-musl/app", syslist.ArchTypeAmd64, "unknown", "app-1.0-x86_64.tar.gz", false},
		{"no platform in path", "bin/app", syslist.ArchTypeArm64, "glibc", "app-1.0-aarch64-linux-musl.tar.gz", false},
		{"gnu in the app's own directory", "gnu-tools/bin/tool-cli", syslist.ArchTypeAmd64, "musl", "tool-2.0-linux-musl.tar.gz", false},
		{"musl in the app's own directory", "musl-tools/bin/tool-cli", syslist.ArchTypeAmd64, "glibc", "tool-2.0-linux-amd64.tar.gz", false},
		{"alpine in the app's own directory", "alpine-tools/bin/tool-cli", syslist.ArchTypeAmd64, "glibc", "tool-2.0-linux-amd64.tar.gz", false},
		{"version digits are not an arch", "tool-1.386.0/bin/tool-cli", syslist.ArchTypeAmd64, "glibc", "tool-1.386.0-linux-amd64.tar.gz", false},
		{"win32 beside the arch is Windows, not 386", "app-2.0-win32-x64/app.exe", syslist.ArchTypeAmd64, "unknown", "app-2.0-win32-x64.zip", false},
		{"musl in a triple", "app-1.0-x86_64-unknown-linux-musl/app", syslist.ArchTypeAmd64, "glibc", "app-1.0-x86_64-unknown-linux-gnu.tar.gz", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pathContradictsAsset(tt.path, tt.arch, tt.libc, tt.filename); got != tt.want {
				t.Errorf("pathContradictsAsset(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDetectBinaryPath_NonArchiveTypes(t *testing.T) {
	for _, ct := range []binmanager.BinContentType{binmanager.BinContentTypeBinary, binmanager.BinContentTypeGz} {
		if got := DetectBinaryPath("app", "app", ct, syslist.OsTypeLinux); got != nil {
			t.Errorf("DetectBinaryPath(content=%s) = %q, want nil", ct, *got)
		}
	}
}

func TestDetectBinaryPath_Heuristic(t *testing.T) {
	tests := []struct {
		name     string
		appName  string
		filename string
		osType   syslist.OsType
		want     string
	}{
		{
			name:     "no version, linux uses appName",
			appName:  "mytool",
			filename: "mytool.tar.gz",
			osType:   syslist.OsTypeLinux,
			want:     "mytool",
		},
		{
			name:     "windows adds .exe",
			appName:  "mytool",
			filename: "mytool.zip",
			osType:   syslist.OsTypeWindows,
			want:     "mytool.exe",
		},
		{
			name:     "versioned filename prefers versioned nested path",
			appName:  "mytool",
			filename: "mytool-1.2.3.tar.gz",
			osType:   syslist.OsTypeLinux,
			want:     "mytool-1.2.3/mytool",
		},
		{
			name:     "versioned windows adds .exe to versioned path",
			appName:  "mytool",
			filename: "mytool-1.2.3.zip",
			osType:   syslist.OsTypeWindows,
			want:     "mytool-1.2.3/mytool.exe",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectBinaryPath(tt.appName, tt.filename, binmanager.BinContentTypeTarGz, tt.osType)
			if derefOr(got, "<nil>") != tt.want {
				t.Errorf("DetectBinaryPath = %q, want %q", derefOr(got, "<nil>"), tt.want)
			}
		})
	}
}

func TestDetectBinaryPathWithHistory(t *testing.T) {
	history := binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {
				"glibc": binmanager.BinaryOsArchInfo{
					ContentType: binmanager.BinContentTypeTarGz,
					BinaryPath:  new("mytool-1.0.0/bin/mytool"),
				},
			},
		},
	}

	// Same OS, new version → pattern is substituted from history.
	got := DetectBinaryPathWithHistory("mytool", "mytool-2.0.0.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", history)
	if derefOr(got, "<nil>") != "mytool-2.0.0/bin/mytool" {
		t.Errorf("with history (matching OS) = %q, want %q", derefOr(got, "<nil>"), "mytool-2.0.0/bin/mytool")
	}

	// Same OS, other arch and libc → the layout names no platform, so it is lent.
	got = DetectBinaryPathWithHistory("mytool", "mytool-2.0.0.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, syslist.ArchTypeArm64, "musl", history)
	if derefOr(got, "<nil>") != "mytool-2.0.0/bin/mytool" {
		t.Errorf("with history (other arch) = %q, want %q", derefOr(got, "<nil>"), "mytool-2.0.0/bin/mytool")
	}

	// Different OS → no history match, falls back to heuristic.
	got = DetectBinaryPathWithHistory("mytool", "mytool-2.0.0.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown", history)
	if derefOr(got, "<nil>") != "mytool-2.0.0/mytool" {
		t.Errorf("with history (other OS) = %q, want heuristic %q", derefOr(got, "<nil>"), "mytool-2.0.0/mytool")
	}

	// Nil history → heuristic.
	got = DetectBinaryPathWithHistory("mytool", "mytool.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", nil)
	if derefOr(got, "<nil>") != "mytool" {
		t.Errorf("nil history = %q, want %q", derefOr(got, "<nil>"), "mytool")
	}
}

func TestHistoricalBinaryPath(t *testing.T) {
	t.Run("empty history", func(t *testing.T) {
		if got := historicalBinaryPath(nil, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", "app-1.0.0.tar.gz"); got != nil {
			t.Errorf("got %q, want nil", *got)
		}
	})

	t.Run("only nil paths returns nil", func(t *testing.T) {
		history := binmanager.MapOfBinaries{
			syslist.OsTypeLinux: {
				syslist.ArchTypeAmd64: {
					"glibc": binmanager.BinaryOsArchInfo{ContentType: binmanager.BinContentTypeBinary, BinaryPath: nil},
				},
			},
		}
		if got := historicalBinaryPath(history, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", "app-1.0.0.tar.gz"); got != nil {
			t.Errorf("got %q, want nil", *got)
		}
	})

	t.Run("no matching OS returns nil", func(t *testing.T) {
		history := binmanager.MapOfBinaries{
			syslist.OsTypeDarwin: {
				syslist.ArchTypeAmd64: {
					"": binmanager.BinaryOsArchInfo{ContentType: binmanager.BinContentTypeTarGz, BinaryPath: new("app-1.0.0/app")},
				},
			},
		}
		if got := historicalBinaryPath(history, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", "app-2.0.0.tar.gz"); got != nil {
			t.Errorf("got %q, want nil", *got)
		}
	})

	t.Run("every layout contradicts the asset returns nil", func(t *testing.T) {
		history := binmanager.MapOfBinaries{
			syslist.OsTypeLinux: {
				syslist.ArchTypeArm64: {
					"glibc": binmanager.BinaryOsArchInfo{ContentType: binmanager.BinContentTypeTarGz, BinaryPath: new("app-1.0.0-arm64/app")},
				},
			},
		}
		if got := historicalBinaryPath(history, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", "app-2.0.0.tar.gz"); got != nil {
			t.Errorf("got %q, want nil", *got)
		}
	})
}

// tombi names each archive's directory after the asset, so darwin/amd64 and
// darwin/arm64 record different directories. A new version must keep each
// architecture's own: taking whichever historical path sorts first handed
// darwin/amd64 the aarch64 directory.
func TestDetectBinaryPathWithHistory_EachArchKeepsItsDirectory(t *testing.T) {
	entry := func(stem string) binmanager.BinaryOsArchInfo {
		return binmanager.BinaryOsArchInfo{
			URL:         "https://example.test/releases/download/v1.5.0/" + stem + ".tar.gz",
			ContentType: binmanager.BinContentTypeTarGz,
			BinaryPath:  new(stem + "/tombi"),
		}
	}
	history := binmanager.MapOfBinaries{
		syslist.OsTypeDarwin: {
			syslist.ArchTypeAmd64: {"unknown": entry("tombi-cli-1.5.0-x86_64-apple-darwin")},
			syslist.ArchTypeArm64: {"unknown": entry("tombi-cli-1.5.0-aarch64-apple-darwin")},
		},
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {
				"glibc": entry("tombi-cli-1.5.0-x86_64-unknown-linux-gnu"),
				"musl":  entry("tombi-cli-1.5.0-x86_64-unknown-linux-musl"),
			},
			syslist.ArchTypeArm64: {
				"glibc": entry("tombi-cli-1.5.0-aarch64-unknown-linux-gnu"),
				"musl":  entry("tombi-cli-1.5.0-aarch64-unknown-linux-musl"),
			},
		},
	}

	tests := []struct {
		os   syslist.OsType
		arch syslist.ArchType
		libc string
		stem string
	}{
		{syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown", "tombi-cli-1.5.5-x86_64-apple-darwin"},
		{syslist.OsTypeDarwin, syslist.ArchTypeArm64, "unknown", "tombi-cli-1.5.5-aarch64-apple-darwin"},
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", "tombi-cli-1.5.5-x86_64-unknown-linux-gnu"},
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl", "tombi-cli-1.5.5-x86_64-unknown-linux-musl"},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc", "tombi-cli-1.5.5-aarch64-unknown-linux-gnu"},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "musl", "tombi-cli-1.5.5-aarch64-unknown-linux-musl"},
	}
	for _, tt := range tests {
		t.Run(tt.stem, func(t *testing.T) {
			got := DetectBinaryPathWithHistory("tombi", tt.stem+".tar.gz",
				binmanager.BinContentTypeTarGz, tt.os, tt.arch, tt.libc, history)
			if want := tt.stem + "/tombi"; derefOr(got, "<nil>") != want {
				t.Errorf("DetectBinaryPathWithHistory = %q, want %q", derefOr(got, "<nil>"), want)
			}
		})
	}
}

// A registry pulled before the fix holds the aarch64 directory under
// darwin/amd64. That entry's layout contradicts the x86_64 asset, so it is
// passed over for the arm64 entry, whose directory is renamed after the asset.
func TestDetectBinaryPathWithHistory_ContradictingLayoutIsSkipped(t *testing.T) {
	history := binmanager.MapOfBinaries{
		syslist.OsTypeDarwin: {
			syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
				URL:         "https://example.test/v1.5.5/tombi-cli-1.5.5-x86_64-apple-darwin.tar.gz",
				ContentType: binmanager.BinContentTypeTarGz,
				BinaryPath:  new("tombi-cli-1.5.5-aarch64-apple-darwin/tombi"),
			}},
			syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
				URL:         "https://example.test/v1.5.5/tombi-cli-1.5.5-aarch64-apple-darwin.tar.gz",
				ContentType: binmanager.BinContentTypeTarGz,
				BinaryPath:  new("tombi-cli-1.5.5-aarch64-apple-darwin/tombi"),
			}},
		},
	}

	got := DetectBinaryPathWithHistory("tombi", "tombi-cli-1.5.6-x86_64-apple-darwin.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown", history)
	if want := "tombi-cli-1.5.6-x86_64-apple-darwin/tombi"; derefOr(got, "<nil>") != want {
		t.Errorf("DetectBinaryPathWithHistory = %q, want %q", derefOr(got, "<nil>"), want)
	}
}

// A version whose digits spell an architecture is still a version: the
// historical layout is kept across it instead of falling back to a guess.
func TestDetectBinaryPathWithHistory_VersionDigitsAreNotAnArch(t *testing.T) {
	history := binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {"glibc": binmanager.BinaryOsArchInfo{
				URL:         "https://example.test/v1.385.0/tool-1.385.0-linux-amd64.tar.gz",
				ContentType: binmanager.BinContentTypeTarGz,
				BinaryPath:  new("tool-1.385.0/bin/tool-cli"),
			}},
		},
	}

	got := DetectBinaryPathWithHistory("tool", "tool-1.386.0-linux-amd64.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", history)
	if want := "tool-1.386.0/bin/tool-cli"; derefOr(got, "<nil>") != want {
		t.Errorf("DetectBinaryPathWithHistory = %q, want %q", derefOr(got, "<nil>"), want)
	}
}

// The musl entry is dropped when it names the same archive as glibc, so a
// libc-neutral asset asked for under musl learns from the glibc entry.
func TestDetectBinaryPathWithHistory_OtherLibcLendsNeutralLayout(t *testing.T) {
	history := binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {"glibc": binmanager.BinaryOsArchInfo{
				URL:         "https://example.test/v1.0.0/app-1.0.0-linux-x86_64.tar.gz",
				ContentType: binmanager.BinContentTypeTarGz,
				BinaryPath:  new("app-1.0.0-linux-x86_64/bin/app"),
			}},
		},
	}

	got := DetectBinaryPathWithHistory("app", "app-1.1.0-linux-x86_64.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl", history)
	if want := "app-1.1.0-linux-x86_64/bin/app"; derefOr(got, "<nil>") != want {
		t.Errorf("DetectBinaryPathWithHistory = %q, want %q", derefOr(got, "<nil>"), want)
	}
}

// buf's release assets carry no version ("buf-Linux-x86_64.tar.gz"), so a tag
// bump has no version to substitute into the historical path. That path must
// survive rather than fall through to the bare app name, which matches the bash
// completion script "buf/etc/bash_completion.d/buf" as well as "buf/bin/buf".
func TestDetectBinaryPathWithHistory_UnversionedAssetKeepsPath(t *testing.T) {
	entry := func(asset string) binmanager.BinaryOsArchInfo {
		return binmanager.BinaryOsArchInfo{
			URL:         "https://example.test/releases/download/v1.72.0/" + asset,
			ContentType: binmanager.BinContentTypeTarGz,
			BinaryPath:  new("buf/bin/buf"),
		}
	}
	history := binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {"glibc": entry("buf-Linux-x86_64.tar.gz")},
			syslist.ArchTypeArm64: {"glibc": entry("buf-Linux-aarch64.tar.gz")},
		},
	}

	tests := []struct {
		name     string
		filename string
	}{
		{"same asset name", "buf-Linux-x86_64.tar.gz"},
		{"renamed asset", "buf-linux-amd64.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectBinaryPathWithHistory("buf", tt.filename,
				binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", history)
			if derefOr(got, "<nil>") != "buf/bin/buf" {
				t.Errorf("DetectBinaryPathWithHistory = %q, want %q", derefOr(got, "<nil>"), "buf/bin/buf")
			}
		})
	}
}

// When each architecture's archive names its binary after the asset, the path
// recorded for the asset with the same name is the one to keep, not another
// architecture's.
func TestDetectBinaryPathWithHistory_SameAssetNameWins(t *testing.T) {
	entry := func(arch string) binmanager.BinaryOsArchInfo {
		return binmanager.BinaryOsArchInfo{
			URL:         "https://example.test/v4.1.0/yq_linux_" + arch + ".tar.gz",
			ContentType: binmanager.BinContentTypeTarGz,
			BinaryPath:  new("./yq_linux_" + arch),
		}
	}
	history := binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {"glibc": entry("amd64")},
			syslist.ArchTypeArm64: {"glibc": entry("arm64")},
		},
	}

	for _, arch := range []syslist.ArchType{syslist.ArchTypeAmd64, syslist.ArchTypeArm64} {
		got := DetectBinaryPathWithHistory("yq", "yq_linux_"+string(arch)+".tar.gz",
			binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, arch, "glibc", history)
		if want := "./yq_linux_" + string(arch); derefOr(got, "<nil>") != want {
			t.Errorf("%s: DetectBinaryPathWithHistory = %q, want %q", arch, derefOr(got, "<nil>"), want)
		}
	}

	// A first build for a new architecture borrows the layout of another one,
	// renamed after the new asset: the path names the asset, not the arch.
	got := DetectBinaryPathWithHistory("yq", "yq_linux_riscv64.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, syslist.ArchTypeRiscv64, "glibc", history)
	if derefOr(got, "<nil>") != "./yq_linux_riscv64" {
		t.Errorf("new asset with per-arch history = %q, want %q", derefOr(got, "<nil>"), "./yq_linux_riscv64")
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://example.test/releases/download/v1.0.0/tool-linux.tar.gz", "tool-linux.tar.gz"},
		{"https://example.test/tool.zip?raw=1", "tool.zip"},
		{"", ""},
		{"://bad", ""},
	}
	for _, tt := range tests {
		if got := assetName(tt.in); got != tt.want {
			t.Errorf("assetName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
