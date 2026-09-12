package cmd

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func bunTestRelease() *github.Release {
	assets := make([]github.Asset, 0, len(bunArchiveSpecs()))
	for _, spec := range bunArchiveSpecs() {
		assets = append(assets, github.Asset{
			Name:               spec.filename,
			BrowserDownloadURL: "https://example.com/" + spec.filename,
			Digest:             "sha256:" + strings.Repeat("a", 64),
		})
	}
	return &github.Release{TagName: "bun-v1.4.1", Assets: assets}
}

func TestDetectBunBinaries(t *testing.T) {
	binaries, err := detectBunBinaries(bunTestRelease())
	if err != nil {
		t.Fatalf("detectBunBinaries() error = %v", err)
	}

	count := 0
	for _, archMap := range binaries {
		for _, libcMap := range archMap {
			count += len(libcMap)
		}
	}
	if count != len(bunArchiveSpecs()) {
		t.Fatalf("detected %d archives, want %d", count, len(bunArchiveSpecs()))
	}

	darwin := binaries[syslist.OsTypeDarwin][syslist.ArchTypeArm64]["unknown"]
	if darwin.ContentType != binmanager.BinContentTypeZip || !darwin.ExtractDir {
		t.Errorf("Darwin archive = %+v, want extracted zip", darwin)
	}
	if darwin.BinaryPath == nil || *darwin.BinaryPath != "bun-darwin-aarch64/bun" {
		t.Errorf("Darwin binaryPath = %v", darwin.BinaryPath)
	}

	windows := binaries[syslist.OsTypeWindows][syslist.ArchTypeAmd64]["unknown"]
	if windows.BinaryPath == nil || *windows.BinaryPath != "bun-windows-x64/bun.exe" {
		t.Errorf("Windows binaryPath = %v", windows.BinaryPath)
	}
}

func TestDetectBunBinariesRejectsMissingAssetOrDigest(t *testing.T) {
	release := bunTestRelease()
	release.Assets = release.Assets[1:]
	if _, err := detectBunBinaries(release); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing asset error = %v", err)
	}

	release = bunTestRelease()
	release.Assets[0].Digest = ""
	if _, err := detectBunBinaries(release); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("missing digest error = %v", err)
	}
}

func TestBuildBunRuntimeJSON(t *testing.T) {
	binaries, err := detectBunBinaries(bunTestRelease())
	if err != nil {
		t.Fatalf("detectBunBinaries() error = %v", err)
	}
	runtimeJSON := buildBunRuntimeJSON(&BunRuntimeData{BunVersion: "1.4.1"}, binaries)
	if runtimeJSON.Kind != "bun" || runtimeJSON.Mode != "managed" {
		t.Errorf("runtime = %+v, want managed Bun", runtimeJSON)
	}
	if runtimeJSON.Bun == nil || runtimeJSON.Bun.BunVersion != "1.4.1" {
		t.Errorf("Bun config = %+v", runtimeJSON.Bun)
	}
	if runtimeJSON.Bun.PNPMRuntime != defaultPNPMRuntimeName {
		t.Errorf("Bun pnpmRuntime = %q, want %q", runtimeJSON.Bun.PNPMRuntime, defaultPNPMRuntimeName)
	}
	// The pnpm version belongs to the pnpm runtime entry, not to Bun's.
	if got := runtimeVersion(runtimeJSON); !strings.Contains(got, "bun=1.4.1") || strings.Contains(got, "pnpm=") {
		t.Errorf("runtimeVersion() = %q", got)
	}
}
