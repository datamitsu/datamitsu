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

func indexOf(names []string, target string) int {
	for i, n := range names {
		if n == target {
			return i
		}
	}
	return -1
}
