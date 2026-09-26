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

	candidates, err := DetectBinaryCandidates(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
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
	best, err := DetectBinary(assets, syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc")
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
			_, err := DetectBinaryCandidates(tt.assets, syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc")
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
			got, err := DetectBinary(assets, tt.os, tt.arch, tt.libc)
			if err != nil {
				t.Fatalf("DetectBinary error = %v", err)
			}
			if got.Name != tt.want {
				t.Errorf("DetectBinary = %q, want %q", got.Name, tt.want)
			}
		})
	}

	candidates, err := DetectBinaryCandidates(assets, syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown")
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
