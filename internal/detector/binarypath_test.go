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

func TestFindCommonPattern(t *testing.T) {
	tests := []struct {
		name        string
		paths       []string
		newFilename string
		want        string // expected deref; "" means nil
	}{
		{"empty paths", nil, "app-2.0.0.tar.gz", ""},
		{"no version in filename", []string{"app-1.0.0/app"}, "app.tar.gz", ""},
		{"same version returns path", []string{"app-1.2.3/app"}, "app-1.2.3.tar.gz", "app-1.2.3/app"},
		{"different version substitutes", []string{"app-1.0.0/bin/app"}, "app-2.0.0.tar.gz", "app-2.0.0/bin/app"},
		{"no version in any path falls back to first", []string{"bin/app"}, "app-2.0.0.tar.gz", "bin/app"},
		{"no version anywhere reuses the agreed path", []string{"buf/bin/buf", "buf/bin/buf"}, "buf-Linux-x86_64.tar.gz", "buf/bin/buf"},
		{"no version, paths disagree", []string{"app/app", "app/bin/app"}, "app-Linux-x86_64.tar.gz", ""},
		{"no version, path names a platform", []string{"yq_linux_amd64"}, "yq_linux_arm64.tar.gz", ""},
		{"no version, path names an os", []string{"app-linux/app"}, "app-Linux-x86_64.tar.gz", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findCommonPattern(tt.paths, tt.newFilename)
			if tt.want == "" {
				if got != nil {
					t.Errorf("findCommonPattern = %q, want nil", *got)
				}
				return
			}
			if derefOr(got, "<nil>") != tt.want {
				t.Errorf("findCommonPattern = %q, want %q", derefOr(got, "<nil>"), tt.want)
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
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, history)
	if derefOr(got, "<nil>") != "mytool-2.0.0/bin/mytool" {
		t.Errorf("with history (matching OS) = %q, want %q", derefOr(got, "<nil>"), "mytool-2.0.0/bin/mytool")
	}

	// Different OS → no history match, falls back to heuristic.
	got = DetectBinaryPathWithHistory("mytool", "mytool-2.0.0.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeDarwin, history)
	if derefOr(got, "<nil>") != "mytool-2.0.0/mytool" {
		t.Errorf("with history (other OS) = %q, want heuristic %q", derefOr(got, "<nil>"), "mytool-2.0.0/mytool")
	}

	// Nil history → heuristic.
	got = DetectBinaryPathWithHistory("mytool", "mytool.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, nil)
	if derefOr(got, "<nil>") != "mytool" {
		t.Errorf("nil history = %q, want %q", derefOr(got, "<nil>"), "mytool")
	}
}

func TestExtractBinaryPathPattern(t *testing.T) {
	t.Run("empty history", func(t *testing.T) {
		if got := extractBinaryPathPattern(nil, syslist.OsTypeLinux, "app-1.0.0.tar.gz"); got != nil {
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
		if got := extractBinaryPathPattern(history, syslist.OsTypeLinux, "app-1.0.0.tar.gz"); got != nil {
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
		if got := extractBinaryPathPattern(history, syslist.OsTypeLinux, "app-2.0.0.tar.gz"); got != nil {
			t.Errorf("got %q, want nil", *got)
		}
	})
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
				binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, history)
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

	for _, arch := range []string{"amd64", "arm64"} {
		got := DetectBinaryPathWithHistory("yq", "yq_linux_"+arch+".tar.gz",
			binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, history)
		if want := "./yq_linux_" + arch; derefOr(got, "<nil>") != want {
			t.Errorf("%s: DetectBinaryPathWithHistory = %q, want %q", arch, derefOr(got, "<nil>"), want)
		}
	}

	got := DetectBinaryPathWithHistory("yq", "yq_linux_riscv64.tar.gz",
		binmanager.BinContentTypeTarGz, syslist.OsTypeLinux, history)
	if derefOr(got, "<nil>") != "yq" {
		t.Errorf("new asset with per-arch history = %q, want heuristic %q", derefOr(got, "<nil>"), "yq")
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
