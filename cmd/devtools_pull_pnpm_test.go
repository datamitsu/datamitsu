package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

const pnpmTestReleaseURL = "https://github.com/pnpm/pnpm/releases/download/v12.4.1/"

// testPNPMBinaries is a minimal valid pnpm pin for fixtures that only need a
// pnpm runtime to exist.
func testPNPMBinaries() binmanager.MapOfBinaries {
	binaryPath := "pnpm"
	return binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {
				"glibc": {
					URL:         pnpmTestReleaseURL + "pnpm-linux-x64.tar.gz",
					Hash:        testHash1,
					ContentType: binmanager.BinContentTypeTarGz,
					BinaryPath:  &binaryPath,
					ExtractDir:  true,
				},
			},
		},
	}
}

// testPNPMRuntime is a valid managed pnpm runtime for fixtures whose Node or Bun
// runtime references it: validation rejects a pnpmRuntime that names nothing.
func testPNPMRuntime() config.RuntimeConfig {
	return config.RuntimeConfig{
		Kind:    config.RuntimeKindPNPM,
		Mode:    config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{Binaries: testPNPMBinaries()},
		PNPM:    &config.RuntimeConfigPNPM{PNPMVersion: "12.4.1"},
	}
}

// pnpmTestRelease gives every pinned asset a distinct digest, so a mapping that
// pairs a platform with the wrong asset shows up as a hash mismatch.
func pnpmTestRelease() *github.Release {
	assets := make([]github.Asset, 0, len(pnpmArchiveSpecs())+1)
	for i, spec := range pnpmArchiveSpecs() {
		assets = append(assets, github.Asset{
			Name:               spec.filename,
			BrowserDownloadURL: pnpmTestReleaseURL + spec.filename,
			Digest:             "sha256:" + strings.Repeat(fmt.Sprintf("%x", i+1), 64),
		})
	}
	// pnpm publishes platforms no other runtime here covers; they must be ignored.
	assets = append(assets, github.Asset{
		Name:               "pnpm-freebsd-x64.tar.gz",
		BrowserDownloadURL: pnpmTestReleaseURL + "pnpm-freebsd-x64.tar.gz",
		Digest:             "sha256:" + strings.Repeat("f", 64),
	})
	return &github.Release{TagName: "v12.4.1", Assets: assets}
}

func TestDetectPNPMBinaries(t *testing.T) {
	release := pnpmTestRelease()
	binaries, err := detectPNPMBinaries(release)
	if err != nil {
		t.Fatalf("detectPNPMBinaries() error = %v", err)
	}

	digests := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		digests[asset.Name] = strings.TrimPrefix(asset.Digest, "sha256:")
	}

	tests := []struct {
		os          syslist.OsType
		arch        syslist.ArchType
		libc        string
		filename    string
		binaryPath  string
		contentType binmanager.BinContentType
	}{
		{syslist.OsTypeDarwin, syslist.ArchTypeAmd64, "unknown", "pnpm-darwin-x64.tar.gz", "pnpm", binmanager.BinContentTypeTarGz},
		{syslist.OsTypeDarwin, syslist.ArchTypeArm64, "unknown", "pnpm-darwin-arm64.tar.gz", "pnpm", binmanager.BinContentTypeTarGz},
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc", "pnpm-linux-x64.tar.gz", "pnpm", binmanager.BinContentTypeTarGz},
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl", "pnpm-linux-x64-musl.tar.gz", "pnpm", binmanager.BinContentTypeTarGz},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc", "pnpm-linux-arm64.tar.gz", "pnpm", binmanager.BinContentTypeTarGz},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "musl", "pnpm-linux-arm64-musl.tar.gz", "pnpm", binmanager.BinContentTypeTarGz},
		{syslist.OsTypeWindows, syslist.ArchTypeAmd64, "unknown", "pnpm-win32-x64.zip", "pnpm.exe", binmanager.BinContentTypeZip},
		{syslist.OsTypeWindows, syslist.ArchTypeArm64, "unknown", "pnpm-win32-arm64.zip", "pnpm.exe", binmanager.BinContentTypeZip},
	}

	count := 0
	for _, archMap := range binaries {
		for _, libcMap := range archMap {
			count += len(libcMap)
		}
	}
	if count != len(tests) {
		t.Fatalf("detected %d archives, want %d", count, len(tests))
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			info, ok := binaries[tt.os][tt.arch][tt.libc]
			if !ok {
				t.Fatalf("%s/%s/%s: missing entry", tt.os, tt.arch, tt.libc)
			}
			if info.URL != pnpmTestReleaseURL+tt.filename {
				t.Errorf("URL = %q, want %q", info.URL, pnpmTestReleaseURL+tt.filename)
			}
			if info.Hash != digests[tt.filename] {
				t.Errorf("Hash = %q, want %q", info.Hash, digests[tt.filename])
			}
			if info.ContentType != tt.contentType {
				t.Errorf("ContentType = %q, want %q", info.ContentType, tt.contentType)
			}
			if info.BinaryPath == nil || *info.BinaryPath != tt.binaryPath {
				t.Errorf("BinaryPath = %v, want %q", info.BinaryPath, tt.binaryPath)
			}
			// The archive's dist/ holds the node-gyp pnpm builds native deps with.
			if !info.ExtractDir {
				t.Error("ExtractDir = false, want the whole archive extracted")
			}
		})
	}

	rt := config.RuntimeConfig{
		Kind:    config.RuntimeKindPNPM,
		Mode:    config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{Binaries: binaries},
		PNPM:    &config.RuntimeConfigPNPM{PNPMVersion: "12.4.1"},
	}
	if err := config.ValidateRuntimes(config.MapOfRuntimes{"pnpm": rt}); err != nil {
		t.Fatalf("ValidateRuntimes rejected detected pnpm binaries: %v", err)
	}
}

func TestDetectPNPMBinariesRejectsMissingAssetOrDigest(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*github.Release)
		wantErr string
	}{
		{"missing asset", func(r *github.Release) { r.Assets = r.Assets[1:] }, "not found"},
		{"empty digest", func(r *github.Release) { r.Assets[0].Digest = "" }, "digest"},
		{"non-sha256 digest", func(r *github.Release) { r.Assets[0].Digest = "sha512:" + strings.Repeat("a", 128) }, "digest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := pnpmTestRelease()
			tt.mutate(release)
			_, err := detectPNPMBinaries(release)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("detectPNPMBinaries() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestBuildPNPMRuntimeJSON pins the pnpm entry pull-runtimes writes, and that
// it loads together with a Node runtime pointing at it by the default name.
func TestBuildPNPMRuntimeJSON(t *testing.T) {
	binaries, err := detectPNPMBinaries(pnpmTestRelease())
	if err != nil {
		t.Fatalf("detectPNPMBinaries() error = %v", err)
	}
	runtimeJSON := buildPNPMRuntimeJSON(&PNPMRuntimeData{PNPMVersion: "12.4.1"}, binaries)

	if runtimeJSON.Kind != "pnpm" || runtimeJSON.Mode != "managed" {
		t.Errorf("runtime = %+v, want managed pnpm", runtimeJSON)
	}
	if runtimeJSON.PNPM == nil || runtimeJSON.PNPM.PNPMVersion != "12.4.1" {
		t.Errorf("pnpm config = %+v", runtimeJSON.PNPM)
	}
	if runtimeJSON.Managed == nil || len(runtimeJSON.Managed.Binaries) == 0 {
		t.Errorf("managed = %+v, want the detected archives", runtimeJSON.Managed)
	}
	if runtimeJSON.Node != nil || runtimeJSON.Bun != nil {
		t.Error("only the pnpm config should be set for a pnpm runtime")
	}
	if got := runtimeVersion(runtimeJSON); !strings.Contains(got, "pnpm=12.4.1") || !strings.Contains(got, "binaries=8") {
		t.Errorf("runtimeVersion() = %q", got)
	}

	written := RuntimesJSON{
		defaultPNPMRuntimeName: runtimeJSON,
		"node":                 buildNodeRuntimeJSON(&NodeRuntimeData{NodeVersion: "26.2.0"}, make(binmanager.MapOfBinaries)),
	}
	data, err := json.Marshal(written)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var runtimes config.MapOfRuntimes
	if err := json.Unmarshal(data, &runtimes); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := config.ValidateRuntimes(runtimes); err != nil {
		t.Fatalf("ValidateRuntimes rejected pulled runtimes: %v", err)
	}
	if name, ok := runtimes.PNPMRuntimeName(runtimes["node"]); !ok || name != defaultPNPMRuntimeName {
		t.Errorf("PNPMRuntimeName(node) = %q, %v; want %q", name, ok, defaultPNPMRuntimeName)
	}
}

// TestCollectStoreRefsPNPMRuntime pins that pnpm is listed like any managed
// runtime: its archives are runtime-binary entries of the pnpm runtime, and a
// system-mode Node that references it contributes nothing of its own.
func TestCollectStoreRefsPNPMRuntime(t *testing.T) {
	cfg := &config.Config{Runtimes: config.MapOfRuntimes{
		"node": {
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "node"},
			Node:   &config.RuntimeConfigNode{NodeVersion: "26.2.0", PNPMRuntime: "pnpm"},
		},
		"pnpm": testPNPMRuntime(),
	}}

	refs := collectStoreRefs(cfg)
	want := storeRefEntry{
		Kind:     "runtime-binary",
		Name:     "pnpm",
		URL:      pnpmTestReleaseURL + "pnpm-linux-x64.tar.gz",
		Hash:     testHash1,
		Platform: "linux/amd64/glibc",
	}
	if len(refs.HTTPS) != 1 || refs.HTTPS[0] != want {
		t.Fatalf("HTTPS refs = %+v, want exactly %+v", refs.HTTPS, want)
	}
}

// assertEmbeddedPNPMRuntime checks that the embedded default config ships a
// managed pnpm runtime under the default name, pinning every pnpm platform
// datamitsu supports to a release archive of its version, each with a SHA-256.
func assertEmbeddedPNPMRuntime(t *testing.T, runtimes config.MapOfRuntimes) {
	t.Helper()
	rt, ok := runtimes[defaultPNPMRuntimeName]
	if !ok {
		t.Fatal("pnpm runtime not found in config")
	}
	if rt.Kind != config.RuntimeKindPNPM || rt.Mode != config.RuntimeModeManaged {
		t.Errorf("pnpm runtime kind/mode = %q/%q, want %q/%q", rt.Kind, rt.Mode, config.RuntimeKindPNPM, config.RuntimeModeManaged)
	}
	if rt.PNPM == nil || rt.PNPM.PNPMVersion == "" {
		t.Fatal("pnpm runtime pnpmVersion is empty")
	}
	if rt.Managed == nil {
		t.Fatal("pnpm runtime managed config is nil")
	}

	version := rt.PNPM.PNPMVersion
	wantPrefix := "https://github.com/pnpm/pnpm/releases/download/v" + version + "/"
	count := 0
	for osType, archMap := range rt.Managed.Binaries {
		for archType, libcMap := range archMap {
			for libc, info := range libcMap {
				count++
				platform := fmt.Sprintf("%s/%s/%s", osType, archType, libc)
				if !strings.HasPrefix(info.URL, wantPrefix) {
					t.Errorf("pnpm %s: url %q is not a %s release asset", platform, info.URL, version)
				}
				if !isLowerHex64(info.Hash) {
					t.Errorf("pnpm %s: hash %q is not a SHA-256 hex digest", platform, info.Hash)
				}
			}
		}
	}
	if count != len(pnpmArchiveSpecs()) {
		t.Errorf("pnpm runtime pins %d platforms, want %d", count, len(pnpmArchiveSpecs()))
	}
}

func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
