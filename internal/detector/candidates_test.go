package detector

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func asset(name string) github.Asset {
	return github.Asset{Name: name, BrowserDownloadURL: "https://example.test/" + name}
}

// When a release ships both a raw binary and archive variants of the same
// asset (yq, terragrunt), archives outrank the raw binary but the raw binary
// must remain in the candidate list so a caller can fall back to it when
// archive extraction verification fails.
func TestDetectBinaryCandidates_ArchiveRankedBeforeRawFallback(t *testing.T) {
	assets := []github.Asset{
		asset("yq_linux_amd64"),
		asset("yq_linux_amd64.tar.gz"),
		asset("yq_linux_amd64.zip"),
		asset("yq_linux_386"),
		asset("yq_windows_amd64.exe"),
		asset("checksums.txt"),
	}

	candidates, err := DetectBinaryCandidates("", assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
	if err != nil {
		t.Fatalf("DetectBinaryCandidates error = %v", err)
	}

	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.Name
	}

	// The raw binary must be present as a fallback.
	rawIdx := indexOf(names, "yq_linux_amd64")
	if rawIdx < 0 {
		t.Fatalf("raw binary yq_linux_amd64 missing from candidates: %v", names)
	}

	// An archive variant must outrank the raw binary (archive bonus).
	if !strings.HasSuffix(names[0], ".tar.gz") && !strings.HasSuffix(names[0], ".zip") {
		t.Errorf("expected an archive first, got %q (all: %v)", names[0], names)
	}
	if rawIdx == 0 {
		t.Errorf("raw binary ranked first, expected an archive ahead of it: %v", names)
	}

	// Wrong-arch and windows assets must not appear for linux/amd64.
	for _, unwanted := range []string{"yq_linux_386", "yq_windows_amd64.exe", "checksums.txt"} {
		if indexOf(names, unwanted) >= 0 {
			t.Errorf("unexpected asset %q in linux/amd64 candidates: %v", unwanted, names)
		}
	}

	// DetectBinary must agree with the top-ranked candidate.
	best, err := DetectBinary("", assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
	if err != nil {
		t.Fatalf("DetectBinary error = %v", err)
	}
	if best.Name != names[0] {
		t.Errorf("DetectBinary = %q, want top candidate %q", best.Name, names[0])
	}
}

func TestDetectBinaryCandidates_Errors(t *testing.T) {
	tests := []struct {
		name    string
		assets  []github.Asset
		wantSub string
	}{
		{"no assets", nil, "no assets available"},
		{"only checksum/package files", []github.Asset{asset("checksums.txt"), asset("tool_linux_arm64.deb")}, "no valid assets"},
		{"no platform match", []github.Asset{asset("tool_windows_amd64.exe")}, "no matching binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DetectBinaryCandidates("", tt.assets, syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}

// snyk names its Alpine builds "snyk-alpine" and "snyk-alpine-arm64": no
// "linux", and no arch token on the amd64 one. Alpine must count as Linux, or
// "snyk-alpine" matches neither implicit rule and linux/amd64/musl is dropped.
func TestDetectBinary_AlpineOnlyNamesLinux(t *testing.T) {
	assets := []github.Asset{
		asset("snyk-alpine"),
		asset("snyk-alpine-arm64"),
		asset("snyk-linux"),
		asset("snyk-linux-arm64"),
		asset("snyk-macos"),
		asset("snyk-macos-arm64"),
		asset("snyk-win.exe"),
	}

	tests := []struct {
		os   syslist.OsType
		arch syslist.ArchType
		libc string
		want string
	}{
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl", "snyk-alpine"},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "musl", "snyk-alpine-arm64"},
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", "snyk-linux"},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc", "snyk-linux-arm64"},
		{syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown", "snyk-macos"},
		{syslist.OsTypeDarwin, syslist.ArchTypeArm64, "unknown", "snyk-macos-arm64"},
	}
	for _, tt := range tests {
		t.Run(string(tt.os)+"/"+string(tt.arch)+"/"+tt.libc, func(t *testing.T) {
			got, err := DetectBinary("", assets, tt.os, tt.arch, tt.libc)
			if err != nil {
				t.Fatalf("DetectBinary error = %v", err)
			}
			if got.Name != tt.want {
				t.Errorf("DetectBinary = %q, want %q", got.Name, tt.want)
			}
		})
	}

	candidates, err := DetectBinaryCandidates("", assets, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
	if err != nil {
		t.Fatalf("DetectBinaryCandidates error = %v", err)
	}
	for _, c := range candidates {
		if strings.Contains(c.Name, "alpine") {
			t.Errorf("darwin/amd64 candidates include %q", c.Name)
		}
	}
}

func indexOf(names []string, target string) int {
	for i, n := range names {
		if n == target {
			return i
		}
	}
	return -1
}

// harperAssets is Automattic/harper v2.11.0 as published: the CLI, the language
// server, the desktop app with its installer and disk image, and editor plugins.
var harperAssets = []string{
	"harper-alpine-arm64-2.11.0.vsix",
	"harper-alpine-x64-2.11.0.vsix",
	"harper-chrome-plugin.zip",
	"harper-cli-aarch64-apple-darwin.tar.gz",
	"harper-cli-aarch64-unknown-linux-gnu.tar.gz",
	"harper-cli-aarch64-unknown-linux-musl.tar.gz",
	"harper-cli-x86_64-apple-darwin.tar.gz",
	"harper-cli-x86_64-pc-windows-msvc.zip",
	"harper-cli-x86_64-unknown-linux-gnu.tar.gz",
	"harper-cli-x86_64-unknown-linux-musl.tar.gz",
	"harper-darwin-arm64-2.11.0.vsix",
	"harper-darwin-x64-2.11.0.vsix",
	"harper-firefox-plugin.zip",
	"harper-linux-arm64-2.11.0.vsix",
	"harper-linux-armhf-2.11.0.vsix",
	"harper-linux-x64-2.11.0.vsix",
	"harper-ls-aarch64-apple-darwin.tar.gz",
	"harper-ls-aarch64-unknown-linux-gnu.tar.gz",
	"harper-ls-aarch64-unknown-linux-musl.tar.gz",
	"harper-ls-x86_64-apple-darwin.tar.gz",
	"harper-ls-x86_64-pc-windows-msvc.zip",
	"harper-ls-x86_64-unknown-linux-gnu.tar.gz",
	"harper-ls-x86_64-unknown-linux-musl.tar.gz",
	"harper-win32-arm64-2.11.0.vsix",
	"harper-win32-x64-2.11.0.vsix",
	"Harper.app.tar.gz",
	"Harper.app.tar.gz.sig",
	"harper.zip",
	"Harper_2.11.0_universal.dmg",
	"Harper_2.11.0_x64-setup.exe",
}

// One release, three programs: the app's own name decides between them, and
// the desktop app's installer is never a candidate at all.
func TestDetectBinaryCandidates_HarperPrefersTheNamedTool(t *testing.T) {
	assets := make([]github.Asset, 0, len(harperAssets))
	for _, name := range harperAssets {
		assets = append(assets, makeAsset(name))
	}

	tests := []struct {
		app  string
		os   syslist.OsType
		arch syslist.ArchType
		libc string
		want string
	}{
		{"harper-cli", syslist.OsTypeWindows, syslist.ArchTypeAmd64, "", "harper-cli-x86_64-pc-windows-msvc.zip"},
		{"harper-ls", syslist.OsTypeWindows, syslist.ArchTypeAmd64, "", "harper-ls-x86_64-pc-windows-msvc.zip"},
		{"harper-cli", syslist.OsTypeDarwin, syslist.ArchTypeArm64, "", "harper-cli-aarch64-apple-darwin.tar.gz"},
		{"harper-cli", syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "", "harper-cli-x86_64-apple-darwin.tar.gz"},
		{"harper-cli", syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl", "harper-cli-x86_64-unknown-linux-musl.tar.gz"},
		{"harper-cli", syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc", "harper-cli-aarch64-unknown-linux-gnu.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.app+" "+string(tt.os)+"/"+string(tt.arch)+"/"+tt.libc, func(t *testing.T) {
			candidates, err := DetectBinaryCandidates(tt.app, assets, tt.os, tt.arch, tt.libc)
			if err != nil {
				t.Fatal(err)
			}
			if candidates[0].Name != tt.want {
				t.Errorf("first candidate = %q, want %q", candidates[0].Name, tt.want)
			}
			for _, c := range candidates {
				if IsNonExecutableFile(c.Name) || IsAttestationFile(c.Name) || IsInstallerFile(tt.app, c.Name) {
					t.Errorf("%q is a candidate", c.Name)
				}
			}
		})
	}
}

// The name preference outranks format: a sibling tool's raw Windows executable
// does not beat the app's own archive.
func TestDetectBinaryCandidates_NameOutranksFormat(t *testing.T) {
	assets := []github.Asset{
		makeAsset("harper-ls-x86_64-pc-windows-msvc.exe"),
		makeAsset("harper-cli-x86_64-pc-windows-msvc.zip"),
	}
	candidates, err := DetectBinaryCandidates("harper-cli", assets, syslist.OsTypeWindows, syslist.ArchTypeAmd64, "")
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Name != "harper-cli-x86_64-pc-windows-msvc.zip" {
		t.Errorf("first candidate = %q, want the app's own archive", candidates[0].Name)
	}
	// Without an app name the executable keeps its priority.
	candidates, err = DetectBinaryCandidates("", assets, syslist.OsTypeWindows, syslist.ArchTypeAmd64, "")
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Name != "harper-ls-x86_64-pc-windows-msvc.exe" {
		t.Errorf("first candidate without an app name = %q, want the executable", candidates[0].Name)
	}
}

// The right libc outranks the app's name: a build that names musl is the
// musl build even when only the glibc one carries the app's name, and a
// glibc binary recorded for musl fails at run time.
func TestDetectBinaryCandidates_LibcOutranksName(t *testing.T) {
	assets := []github.Asset{
		makeAsset("tool-cli-linux-amd64.tar.gz"),
		makeAsset("tool-alpine-amd64.tar.gz"),
	}
	candidates, err := DetectBinaryCandidates("tool-cli", assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl")
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Name != "tool-alpine-amd64.tar.gz" {
		t.Errorf("musl candidate = %q, want the alpine build", candidates[0].Name)
	}
	candidates, err = DetectBinaryCandidates("tool-cli", assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Name != "tool-cli-linux-amd64.tar.gz" {
		t.Errorf("glibc candidate = %q, want the libc-neutral build named after the app", candidates[0].Name)
	}
}
