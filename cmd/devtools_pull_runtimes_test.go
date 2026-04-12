package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRuntimeBinaries_BasicDetection(t *testing.T) {
	assets := []github.Asset{
		{Name: "runtime-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-amd64", Digest: "sha256:" + testHash1},
		{Name: "runtime-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-arm64", Digest: "sha256:" + testHash2},
		{Name: "runtime-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64", Digest: "sha256:" + testHash3},
		{Name: "runtime-linux-arm64.tar.gz", BrowserDownloadURL: "https://example.com/linux-arm64", Digest: "sha256:" + testHash4},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("runtime", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if binaries == nil {
		t.Fatal("expected non-nil binaries")
	}

	// Should have darwin entries
	if binaries[syslist.OsTypeDarwin] == nil {
		t.Error("expected darwin entries")
	}

	// Should have linux entries
	if binaries[syslist.OsTypeLinux] == nil {
		t.Error("expected linux entries")
	}
}

func TestDetectRuntimeBinaries_DeduplicatesSingleLinuxBinary(t *testing.T) {
	// When a single Linux binary exists per arch (no libc indicator),
	// deduplication should keep only the glibc entry.
	assets := []github.Asset{
		{Name: "runtime-linux-amd64.zip", BrowserDownloadURL: "https://example.com/linux-amd64.zip", Digest: "sha256:" + testHash1},
		{Name: "runtime-linux-arm64.zip", BrowserDownloadURL: "https://example.com/linux-arm64.zip", Digest: "sha256:" + testHash2},
		{Name: "runtime-darwin-amd64.zip", BrowserDownloadURL: "https://example.com/darwin-amd64.zip", Digest: "sha256:" + testHash3},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("runtime", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]

	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}
	if _, ok := linuxAmd64["musl"]; ok {
		t.Error("linux/amd64/musl should be deduplicated (same binary as glibc)")
	}
}

func TestDetectRuntimeBinaries_SeparateMuslBinariesKept(t *testing.T) {
	// When separate glibc and musl binaries exist, both should be kept.
	assets := []github.Asset{
		{Name: "runtime-linux-gnu-amd64.tar.gz", BrowserDownloadURL: "https://example.com/gnu", Digest: "sha256:" + testHash1},
		{Name: "runtime-linux-musl-amd64.tar.gz", BrowserDownloadURL: "https://example.com/musl", Digest: "sha256:" + testHash2},
		{Name: "runtime-linux-gnu-arm64.tar.gz", BrowserDownloadURL: "https://example.com/gnu-arm", Digest: "sha256:" + testHash3},
		{Name: "runtime-linux-musl-arm64.tar.gz", BrowserDownloadURL: "https://example.com/musl-arm", Digest: "sha256:" + testHash4},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("runtime", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}
	if _, ok := linuxAmd64["musl"]; !ok {
		t.Error("expected linux/amd64/musl entry (separate binary)")
	}

	// Verify different URLs
	if linuxAmd64["glibc"].URL == linuxAmd64["musl"].URL {
		t.Error("glibc and musl should have different URLs")
	}
}

func TestDetectRuntimeBinaries_NoDuplicateMuslKeys(t *testing.T) {
	// When the same binary is detected for both glibc and musl,
	// only the glibc entry should exist (no duplicate URL+hash pairs).
	assets := []github.Asset{
		{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64", Digest: "sha256:" + testHash1},
		{Name: "tool-linux-arm64.tar.gz", BrowserDownloadURL: "https://example.com/linux-arm64", Digest: "sha256:" + testHash2},
		{Name: "tool-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-amd64", Digest: "sha256:" + testHash3},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("tool", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no duplicate URL+hash pairs per OS/arch
	type urlHash struct {
		url  string
		hash string
	}
	seen := make(map[string][]urlHash)
	for osType, archMap := range binaries {
		for archType, libcMap := range archMap {
			key := string(osType) + "/" + string(archType)
			for _, info := range libcMap {
				seen[key] = append(seen[key], urlHash{info.URL, info.Hash})
			}
		}
	}

	for key, pairs := range seen {
		for i := 0; i < len(pairs); i++ {
			for j := i + 1; j < len(pairs); j++ {
				if pairs[i].url == pairs[j].url && pairs[i].hash == pairs[j].hash {
					t.Errorf("%s has duplicate URL+hash: %s", key, pairs[i].url)
				}
			}
		}
	}
}

func TestDetectRuntimeBinaries_MissingHashReturnsError(t *testing.T) {
	assets := []github.Asset{
		{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux", Digest: ""},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	_, err := detectRuntimeBinaries("tool", release)
	if err == nil {
		t.Fatal("expected error for missing hash")
	}
}

func TestDetectRuntimeBinaries_NoMatchingAssetsReturnsError(t *testing.T) {
	assets := []github.Asset{
		{Name: "completely-unrelated.txt", BrowserDownloadURL: "https://example.com/txt", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	_, err := detectRuntimeBinaries("tool", release)
	if err == nil {
		t.Fatal("expected error for no matching assets")
	}
}

func TestDetectRuntimeBinaries_HashExtraction(t *testing.T) {
	assets := []github.Asset{
		{Name: "tool-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("tool", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info := binaries[syslist.OsTypeDarwin][syslist.ArchTypeAmd64]["unknown"]
	if info.Hash != testHash1 {
		t.Errorf("hash = %q, want %q", info.Hash, testHash1)
	}
}

func TestDetectRuntimeBinaries_ContentTypeDetection(t *testing.T) {
	assets := []github.Asset{
		{Name: "tool-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/zip", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("tool", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info := binaries[syslist.OsTypeDarwin][syslist.ArchTypeArm64]["unknown"]
	if info.ContentType != binmanager.BinContentTypeZip {
		t.Errorf("ContentType = %v, want zip", info.ContentType)
	}
}



func TestDetectRuntimeBinaries_UVSeparateMuslBinaries(t *testing.T) {
	// UV has separate gnu and musl tarballs — both should be kept
	assets := []github.Asset{
		{Name: "uv-x86_64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/uv-darwin-amd64", Digest: "sha256:" + testHash1},
		{Name: "uv-aarch64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/uv-darwin-arm64", Digest: "sha256:" + testHash2},
		{Name: "uv-x86_64-unknown-linux-gnu.tar.gz", BrowserDownloadURL: "https://example.com/uv-linux-gnu-amd64", Digest: "sha256:" + testHash3},
		{Name: "uv-x86_64-unknown-linux-musl.tar.gz", BrowserDownloadURL: "https://example.com/uv-linux-musl-amd64", Digest: "sha256:" + testHash4},
		{Name: "uv-aarch64-unknown-linux-gnu.tar.gz", BrowserDownloadURL: "https://example.com/uv-linux-gnu-arm64", Digest: "sha256:" + testHash5},
	}

	release := &github.Release{
		TagName: "0.10.9",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("uv", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// linux/amd64 should have BOTH glibc and musl (separate files)
	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry for UV")
	}
	if _, ok := linuxAmd64["musl"]; !ok {
		t.Error("expected linux/amd64/musl entry for UV (separate binary)")
	}

	// Verify different URLs for glibc vs musl
	if linuxAmd64["glibc"].URL == linuxAmd64["musl"].URL {
		t.Error("UV glibc and musl should have different URLs")
	}

	// darwin should use "unknown" libc
	darwinAmd64 := binaries[syslist.OsTypeDarwin][syslist.ArchTypeAmd64]
	if _, ok := darwinAmd64["unknown"]; !ok {
		t.Error("expected darwin/amd64/unknown entry")
	}
}

func TestDetectRuntimeBinaries_UVDeduplicationSkipsWhenSameFile(t *testing.T) {
	// If UV somehow had the same file for both gnu and musl (hypothetical),
	// deduplication should skip the musl entry
	assets := []github.Asset{
		{Name: "uv-x86_64-unknown-linux.tar.gz", BrowserDownloadURL: "https://example.com/uv-linux-amd64", Digest: "sha256:" + testHash1},
		{Name: "uv-aarch64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/uv-darwin-arm64", Digest: "sha256:" + testHash2},
	}

	release := &github.Release{
		TagName: "0.10.9",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("uv", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}
	if _, ok := linuxAmd64["musl"]; ok {
		t.Error("linux/amd64/musl should be deduplicated (same binary as glibc)")
	}
}

func TestDetectRuntimeBinaries_UVLibcMismatchRejection(t *testing.T) {
	// When only musl assets exist for Linux, glibc tuple should NOT get the musl asset
	assets := []github.Asset{
		{Name: "uv-x86_64-unknown-linux-musl.tar.gz", BrowserDownloadURL: "https://example.com/uv-musl", Digest: "sha256:" + testHash1},
		{Name: "uv-x86_64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/uv-darwin", Digest: "sha256:" + testHash2},
	}

	release := &github.Release{
		TagName: "0.10.9",
		Assets:  assets,
	}

	binaries, err := detectRuntimeBinaries("uv", release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if linuxAmd64 != nil {
		if _, ok := linuxAmd64["glibc"]; ok {
			t.Error("glibc entry should NOT exist when only musl assets available (libc mismatch rejection)")
		}
		if _, ok := linuxAmd64["musl"]; !ok {
			t.Error("expected linux/amd64/musl entry")
		}
	}
}



func TestDetectJVMBinaries_AlpineLinuxDetectedAsMusl(t *testing.T) {
	assets := []github.Asset{
		{Name: "OpenJDK25U-jdk_x64_mac_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/mac-amd64", Digest: "sha256:" + testHash1},
		{Name: "OpenJDK25U-jdk_aarch64_mac_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/mac-arm64", Digest: "sha256:" + testHash2},
		{Name: "OpenJDK25U-jdk_x64_linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64", Digest: "sha256:" + testHash3},
		{Name: "OpenJDK25U-jdk_x64_alpine-linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/alpine-amd64", Digest: "sha256:" + testHash4},
		{Name: "OpenJDK25U-jdk_aarch64_linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/linux-arm64", Digest: "sha256:" + testHash5},
	}

	release := &github.Release{
		TagName: "jdk-25.0.2+10",
		Assets:  assets,
	}

	binaries, err := detectJVMBinaries(release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// linux/amd64 should have glibc from regular linux asset
	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}

	// linux/amd64 should have musl from alpine-linux asset
	if _, ok := linuxAmd64["musl"]; !ok {
		t.Error("expected linux/amd64/musl entry (alpine-linux)")
	}

	// Verify different URLs for glibc vs musl
	if linuxAmd64["glibc"].URL == linuxAmd64["musl"].URL {
		t.Error("glibc and musl should have different URLs")
	}
}

func TestDetectJVMBinaries_Deduplication(t *testing.T) {
	// If only a single linux binary (no alpine variant), musl should be deduplicated
	assets := []github.Asset{
		{Name: "OpenJDK25U-jdk_x64_linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64", Digest: "sha256:" + testHash1},
		{Name: "OpenJDK25U-jdk_x64_mac_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/mac-amd64", Digest: "sha256:" + testHash2},
	}

	release := &github.Release{
		TagName: "jdk-25.0.2+10",
		Assets:  assets,
	}

	binaries, err := detectJVMBinaries(release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}
	if _, ok := linuxAmd64["musl"]; ok {
		t.Error("linux/amd64/musl should be deduplicated when no separate alpine binary exists")
	}
}

func TestDetectJVMBinaries_ExtractDirSetCorrectly(t *testing.T) {
	assets := []github.Asset{
		{Name: "OpenJDK25U-jdk_x64_mac_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/mac-amd64", Digest: "sha256:" + testHash1},
		{Name: "OpenJDK25U-jdk_x64_linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64", Digest: "sha256:" + testHash2},
		{Name: "OpenJDK25U-jdk_x64_alpine-linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/alpine-amd64", Digest: "sha256:" + testHash3},
	}

	release := &github.Release{
		TagName: "jdk-25.0.2+10",
		Assets:  assets,
	}

	binaries, err := detectJVMBinaries(release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for osType, archMap := range binaries {
		for archType, libcMap := range archMap {
			for libc, info := range libcMap {
				if !info.ExtractDir {
					t.Errorf("%s/%s/%s: ExtractDir should be true", osType, archType, libc)
				}
			}
		}
	}
}

func TestDetectJVMBinaries_BinaryPathDetection(t *testing.T) {
	assets := []github.Asset{
		{Name: "OpenJDK25U-jdk_x64_mac_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/mac-amd64", Digest: "sha256:" + testHash1},
		{Name: "OpenJDK25U-jdk_x64_linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64", Digest: "sha256:" + testHash2},
		{Name: "OpenJDK25U-jdk_x64_alpine-linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/alpine-amd64", Digest: "sha256:" + testHash3},
	}

	release := &github.Release{
		TagName: "jdk-25.0.2+10",
		Assets:  assets,
	}

	binaries, err := detectJVMBinaries(release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// macOS: binaryPath should include Contents/Home
	macInfo := binaries[syslist.OsTypeDarwin][syslist.ArchTypeAmd64]["unknown"]
	if macInfo.BinaryPath == nil {
		t.Fatal("expected non-nil BinaryPath for macOS")
	}
	wantMacPath := "jdk-25.0.2+10/Contents/Home/bin/java"
	if *macInfo.BinaryPath != wantMacPath {
		t.Errorf("macOS BinaryPath = %q, want %q", *macInfo.BinaryPath, wantMacPath)
	}

	// Linux glibc
	linuxInfo := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]["glibc"]
	if linuxInfo.BinaryPath == nil {
		t.Fatal("expected non-nil BinaryPath for Linux glibc")
	}
	wantLinuxPath := "jdk-25.0.2+10/bin/java"
	if *linuxInfo.BinaryPath != wantLinuxPath {
		t.Errorf("Linux glibc BinaryPath = %q, want %q", *linuxInfo.BinaryPath, wantLinuxPath)
	}

	// Linux musl (alpine) should have same binaryPath pattern as linux glibc
	muslInfo := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]["musl"]
	if muslInfo.BinaryPath == nil {
		t.Fatal("expected non-nil BinaryPath for Linux musl")
	}
	if *muslInfo.BinaryPath != wantLinuxPath {
		t.Errorf("Linux musl BinaryPath = %q, want %q", *muslInfo.BinaryPath, wantLinuxPath)
	}
}

func TestDetectJVMBinaries_NoMatchingAssetsReturnsError(t *testing.T) {
	assets := []github.Asset{
		{Name: "unrelated-file.txt", BrowserDownloadURL: "https://example.com/txt", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{
		TagName: "jdk-25.0.2+10",
		Assets:  assets,
	}

	_, err := detectJVMBinaries(release)
	if err == nil {
		t.Fatal("expected error for no matching JDK assets")
	}
}

func TestJvmBinaryPath(t *testing.T) {
	tests := []struct {
		tag    string
		osType syslist.OsType
		want   string
	}{
		{"jdk-25.0.2+10", syslist.OsTypeDarwin, "jdk-25.0.2+10/Contents/Home/bin/java"},
		{"jdk-25.0.2+10", syslist.OsTypeLinux, "jdk-25.0.2+10/bin/java"},
		{"jdk-25.0.2+10", syslist.OsTypeWindows, "jdk-25.0.2+10/bin/java.exe"},
		{"jdk-21.0.5+11", syslist.OsTypeDarwin, "jdk-21.0.5+11/Contents/Home/bin/java"},
		{"jdk-21.0.5+11", syslist.OsTypeLinux, "jdk-21.0.5+11/bin/java"},
	}

	for _, tt := range tests {
		got := jvmBinaryPath(tt.tag, tt.osType)
		if got != tt.want {
			t.Errorf("jvmBinaryPath(%q, %q) = %q, want %q", tt.tag, tt.osType, got, tt.want)
		}
	}
}

func buildTestRuntimes() RuntimesJSON {
	bp := "fnm"
	uvBP := "uv-x86_64-apple-darwin/uv"
	jdkBP := "jdk-25.0.2+10/bin/java"
	jdkMacBP := "jdk-25.0.2+10/Contents/Home/bin/java"

	return RuntimesJSON{
		"fnm": buildFNMRuntimeJSON(
			&FNMRuntimeData{
				NodeVersion: "24.14.0",
				PNPMVersion: "10.31.0",
				PNPMHash:    testHash1,
			},
			binmanager.MapOfBinaries{
				syslist.OsTypeDarwin: {
					syslist.ArchTypeAmd64: {
						"unknown": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/fnm-macos.zip", Hash: testHash2,
							ContentType: binmanager.BinContentTypeZip, BinaryPath: &bp,
						},
					},
				},
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {
						"glibc": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/fnm-linux.zip", Hash: testHash3,
							ContentType: binmanager.BinContentTypeZip, BinaryPath: &bp,
						},
					},
				},
			},
		),
		"uv": buildUVRuntimeJSON(
			&UVRuntimeData{PythonVersion: "3.14.3"},
			binmanager.MapOfBinaries{
				syslist.OsTypeDarwin: {
					syslist.ArchTypeAmd64: {
						"unknown": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/uv-darwin.tar.gz", Hash: testHash1,
							ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &uvBP,
						},
					},
				},
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {
						"glibc": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/uv-gnu.tar.gz", Hash: testHash2,
							ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &uvBP,
						},
						"musl": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/uv-musl.tar.gz", Hash: testHash3,
							ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &uvBP,
						},
					},
				},
			},
		),
		"jvm": buildJVMRuntimeJSON(
			&JVMRuntimeData{JavaVersion: "25"},
			binmanager.MapOfBinaries{
				syslist.OsTypeDarwin: {
					syslist.ArchTypeAmd64: {
						"unknown": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/jdk-mac.tar.gz", Hash: testHash1,
							ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &jdkMacBP,
							ExtractDir: true,
						},
					},
				},
				syslist.OsTypeLinux: {
					syslist.ArchTypeAmd64: {
						"glibc": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/jdk-linux.tar.gz", Hash: testHash2,
							ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &jdkBP,
							ExtractDir: true,
						},
						"musl": binmanager.BinaryOsArchInfo{
							URL: "https://example.com/jdk-alpine.tar.gz", Hash: testHash3,
							ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &jdkBP,
							ExtractDir: true,
						},
					},
				},
			},
		),
	}
}

func TestWriteRuntimesJSON_StructureValidity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	runtimes := buildTestRuntimes()

	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	// Verify it's valid JSON by unmarshaling back
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Must have all three runtime keys
	for _, key := range []string{"fnm", "uv", "jvm"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}

	// Verify each runtime has kind and mode
	for _, key := range []string{"fnm", "uv", "jvm"} {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(parsed[key], &entry); err != nil {
			t.Fatalf("parsing %s: %v", key, err)
		}
		if _, ok := entry["kind"]; !ok {
			t.Errorf("%s: missing 'kind' field", key)
		}
		if _, ok := entry["mode"]; !ok {
			t.Errorf("%s: missing 'mode' field", key)
		}
		if _, ok := entry["managed"]; !ok {
			t.Errorf("%s: missing 'managed' field", key)
		}
	}

	// Verify runtime-specific config keys
	var fnmEntry map[string]json.RawMessage
	_ = json.Unmarshal(parsed["fnm"], &fnmEntry)
	if _, ok := fnmEntry["fnm"]; !ok {
		t.Error("fnm runtime missing 'fnm' config key")
	}

	var uvEntry map[string]json.RawMessage
	_ = json.Unmarshal(parsed["uv"], &uvEntry)
	if _, ok := uvEntry["uv"]; !ok {
		t.Error("uv runtime missing 'uv' config key")
	}

	var jvmEntry map[string]json.RawMessage
	_ = json.Unmarshal(parsed["jvm"], &jvmEntry)
	if _, ok := jvmEntry["jvm"]; !ok {
		t.Error("jvm runtime missing 'jvm' config key")
	}
}

func TestWriteRuntimesJSON_NestedLibcStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	runtimes := buildTestRuntimes()

	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	// Parse back into structured types to verify nested libc keys
	var parsed RuntimesJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// FNM: linux should have only glibc (no musl)
	fnmLinux := parsed["fnm"].Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := fnmLinux["glibc"]; !ok {
		t.Error("fnm: expected linux/amd64/glibc")
	}
	if _, ok := fnmLinux["musl"]; ok {
		t.Error("fnm: unexpected linux/amd64/musl")
	}

	// UV: linux should have both glibc and musl
	uvLinux := parsed["uv"].Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := uvLinux["glibc"]; !ok {
		t.Error("uv: expected linux/amd64/glibc")
	}
	if _, ok := uvLinux["musl"]; !ok {
		t.Error("uv: expected linux/amd64/musl")
	}

	// JVM: linux should have both glibc and musl
	jvmLinux := parsed["jvm"].Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := jvmLinux["glibc"]; !ok {
		t.Error("jvm: expected linux/amd64/glibc")
	}
	if _, ok := jvmLinux["musl"]; !ok {
		t.Error("jvm: expected linux/amd64/musl")
	}

	// Darwin should use "unknown" libc
	for _, name := range []string{"fnm", "uv", "jvm"} {
		darwinAmd64 := parsed[name].Managed.Binaries[syslist.OsTypeDarwin][syslist.ArchTypeAmd64]
		if _, ok := darwinAmd64["unknown"]; !ok {
			t.Errorf("%s: expected darwin/amd64/unknown", name)
		}
	}

	// JVM entries should preserve ExtractDir=true
	for _, libcKey := range []string{"glibc", "musl"} {
		info := parsed["jvm"].Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64][libcKey]
		if !info.ExtractDir {
			t.Errorf("jvm linux/amd64/%s: ExtractDir should be true", libcKey)
		}
	}
}

func TestWriteRuntimesJSON_Formatting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	runtimes := buildTestRuntimes()

	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	content := string(data)

	// Must end with trailing newline
	if !strings.HasSuffix(content, "\n") {
		t.Error("output should end with trailing newline")
	}

	// Must use 2-space indentation (check for "  " at start of indented lines)
	lines := strings.Split(content, "\n")
	hasIndented := false
	for _, line := range lines {
		if len(line) > 0 && line[0] == ' ' {
			hasIndented = true
			// Indentation should be multiples of 2 spaces
			trimmed := strings.TrimLeft(line, " ")
			indent := len(line) - len(trimmed)
			if indent%2 != 0 {
				t.Errorf("line has odd indentation (%d spaces): %s", indent, line)
			}
		}
	}
	if !hasIndented {
		t.Error("expected indented lines in output")
	}

	// Must NOT use tab indentation
	if strings.Contains(content, "\t") {
		t.Error("output should not contain tabs")
	}

	// Verify file permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("file permissions = %o, want 0644", info.Mode().Perm())
	}
}

func TestWriteRuntimesJSON_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")

	// Write initial content
	runtimes := buildTestRuntimes()
	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	// Overwrite with different content
	runtimes["fnm"].FNM.NodeVersion = "22.0.0"
	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	// Verify updated content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	if !strings.Contains(string(data), "22.0.0") {
		t.Error("overwritten file should contain updated version")
	}

	// No temp files should remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file not cleaned up: %s", e.Name())
		}
	}
}

func TestBuildFNMRuntimeJSON(t *testing.T) {
	data := &FNMRuntimeData{
		NodeVersion: "24.14.0",
		PNPMVersion: "10.31.0",
		PNPMHash:    testHash1,
	}
	binaries := make(binmanager.MapOfBinaries)

	result := buildFNMRuntimeJSON(data, binaries)

	if result.Kind != "fnm" {
		t.Errorf("Kind = %q, want %q", result.Kind, "fnm")
	}
	if result.Mode != "managed" {
		t.Errorf("Mode = %q, want %q", result.Mode, "managed")
	}
	if result.FNM == nil {
		t.Fatal("FNM config should not be nil")
	}
	if result.FNM.NodeVersion != "24.14.0" {
		t.Errorf("NodeVersion = %q, want %q", result.FNM.NodeVersion, "24.14.0")
	}
	if result.UV != nil {
		t.Error("UV config should be nil for FNM runtime")
	}
	if result.JVM != nil {
		t.Error("JVM config should be nil for FNM runtime")
	}
}

func TestBuildUVRuntimeJSON(t *testing.T) {
	data := &UVRuntimeData{PythonVersion: "3.14.3"}
	binaries := make(binmanager.MapOfBinaries)

	result := buildUVRuntimeJSON(data, binaries)

	if result.Kind != "uv" {
		t.Errorf("Kind = %q, want %q", result.Kind, "uv")
	}
	if result.UV == nil {
		t.Fatal("UV config should not be nil")
	}
	if result.UV.PythonVersion != "3.14.3" {
		t.Errorf("PythonVersion = %q, want %q", result.UV.PythonVersion, "3.14.3")
	}
	if result.FNM != nil {
		t.Error("FNM config should be nil for UV runtime")
	}
}

func TestBuildJVMRuntimeJSON(t *testing.T) {
	data := &JVMRuntimeData{JavaVersion: "25"}
	binaries := make(binmanager.MapOfBinaries)

	result := buildJVMRuntimeJSON(data, binaries)

	if result.Kind != "jvm" {
		t.Errorf("Kind = %q, want %q", result.Kind, "jvm")
	}
	if result.JVM == nil {
		t.Fatal("JVM config should not be nil")
	}
	if result.JVM.JavaVersion != "25" {
		t.Errorf("JavaVersion = %q, want %q", result.JVM.JavaVersion, "25")
	}
	if result.FNM != nil {
		t.Error("FNM config should be nil for JVM runtime")
	}
}

// Task 8 tests: main command logic

func TestRunPullRuntimes_RequiresUpdateFlag(t *testing.T) {
	oldFlag := pullRuntimesUpdateFlag
	defer func() { pullRuntimesUpdateFlag = oldFlag }()

	pullRuntimesUpdateFlag = false
	err := runPullRuntimes(nil, []string{"test.json"})
	if err == nil {
		t.Fatal("expected error when --update flag is not set")
	}
	if !strings.Contains(err.Error(), "--update flag is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIsValidRuntime(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"fnm", true},
		{"uv", true},
		{"jvm", true},
		{"invalid", false},
		{"", false},
		{"FNM", false},
	}

	for _, tt := range tests {
		got := isValidRuntime(tt.name)
		if got != tt.want {
			t.Errorf("isValidRuntime(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRuntimeVersion_FNM(t *testing.T) {
	r := &RuntimeJSON{
		FNM: &FNMConfigJSON{
			NodeVersion: "24.14.0",
			PNPMVersion: "10.31.0",
		},
		Managed: &RuntimeManagedJSON{
			Binaries: binmanager.MapOfBinaries{
				syslist.OsTypeDarwin: {
					syslist.ArchTypeAmd64: {
						"unknown": binmanager.BinaryOsArchInfo{URL: "x", Hash: "y"},
					},
				},
			},
		},
	}

	v := runtimeVersion(r)
	if !strings.Contains(v, "node=24.14.0") {
		t.Errorf("expected node version in output, got %q", v)
	}
	if !strings.Contains(v, "pnpm=10.31.0") {
		t.Errorf("expected pnpm version in output, got %q", v)
	}
	if !strings.Contains(v, "binaries=1") {
		t.Errorf("expected binaries count in output, got %q", v)
	}
}

func TestRuntimeVersion_UV(t *testing.T) {
	r := &RuntimeJSON{
		UV: &UVConfigJSON{PythonVersion: "3.14.3"},
	}
	v := runtimeVersion(r)
	if !strings.Contains(v, "python=3.14.3") {
		t.Errorf("expected python version, got %q", v)
	}
}

func TestRuntimeVersion_JVM(t *testing.T) {
	r := &RuntimeJSON{
		JVM: &JVMConfigJSON{JavaVersion: "25"},
	}
	v := runtimeVersion(r)
	if !strings.Contains(v, "java=25") {
		t.Errorf("expected java version, got %q", v)
	}
}

func TestRuntimeVersion_Nil(t *testing.T) {
	v := runtimeVersion(nil)
	if v != "" {
		t.Errorf("expected empty string for nil, got %q", v)
	}
}

func TestReadRuntimesJSON_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	runtimes := buildTestRuntimes()

	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON failed: %v", err)
	}

	read, err := readRuntimesJSON(path)
	if err != nil {
		t.Fatalf("readRuntimesJSON failed: %v", err)
	}

	if len(read) != 3 {
		t.Errorf("expected 3 runtimes, got %d", len(read))
	}
	for _, key := range []string{"fnm", "uv", "jvm"} {
		if _, ok := read[key]; !ok {
			t.Errorf("missing runtime key %q", key)
		}
	}
}

func TestReadRuntimesJSON_MissingFile(t *testing.T) {
	_, err := readRuntimesJSON("/nonexistent/runtimes.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadRuntimesJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := readRuntimesJSON(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRunPullRuntimes_InvalidRuntimeFilter(t *testing.T) {
	oldUpdate := pullRuntimesUpdateFlag
	oldRuntime := pullRuntimesRuntimeFlag
	defer func() {
		pullRuntimesUpdateFlag = oldUpdate
		pullRuntimesRuntimeFlag = oldRuntime
	}()

	pullRuntimesUpdateFlag = true
	pullRuntimesRuntimeFlag = "invalid"

	err := runPullRuntimes(nil, []string{"test.json"})
	if err == nil {
		t.Fatal("expected error for invalid runtime filter")
	}
	if !strings.Contains(err.Error(), "invalid runtime") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadWriteRuntimesJSON_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")

	original := buildTestRuntimes()
	if err := writeRuntimesJSON(path, original); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	read, err := readRuntimesJSON(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	origJSON, _ := json.Marshal(original)
	readJSON, _ := json.Marshal(read)

	if string(origJSON) != string(readJSON) {
		t.Error("roundtrip mismatch: written and read JSON differ")
	}
}

func TestWriteRuntimesJSON_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")

	runtimes := buildTestRuntimes()
	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected exactly 1 file, got %d", len(entries))
	}
	if entries[0].Name() != "runtimes.json" {
		t.Errorf("expected runtimes.json, got %s", entries[0].Name())
	}
}

func TestValidRuntimeNames(t *testing.T) {
	expected := map[string]bool{"fnm": true, "uv": true, "jvm": true}
	for _, name := range validRuntimeNames {
		if !expected[name] {
			t.Errorf("unexpected runtime name: %s", name)
		}
	}
	if len(validRuntimeNames) != 3 {
		t.Errorf("expected 3 valid runtime names, got %d", len(validRuntimeNames))
	}
}

func TestPullRuntimesCommand_RequiresExactlyOneArg(t *testing.T) {
	if pullRuntimesCmd.Args == nil {
		t.Fatal("expected Args validator to be set (cobra.ExactArgs(1))")
	}
	err := pullRuntimesCmd.Args(pullRuntimesCmd, []string{})
	if err == nil {
		t.Fatal("expected error when no file argument provided")
	}
	err = pullRuntimesCmd.Args(pullRuntimesCmd, []string{"file.json"})
	if err != nil {
		t.Fatalf("expected no error with one argument, got: %v", err)
	}
}

func TestPullRuntimesCommand_RejectsMultipleArgs(t *testing.T) {
	if pullRuntimesCmd.Args == nil {
		t.Fatal("expected Args validator to be set (cobra.ExactArgs(1))")
	}
	err := pullRuntimesCmd.Args(pullRuntimesCmd, []string{"file1.json", "file2.json"})
	if err == nil {
		t.Fatal("expected error when multiple file arguments provided")
	}
}


// === Integration Tests (Task 10) ===

func TestIntegration_FullPullFlow(t *testing.T) {
	// End-to-end test: detect binaries from synthetic releases, build JSON
	// structures for all three runtimes, write to file, read back, and verify.
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "runtimes.json")

	// Simulate FNM release (single Linux binary per arch → only glibc)
	fnmRelease := &github.Release{
		TagName: "v1.40.0",
		Assets: []github.Asset{
			{Name: "fnm-darwin-amd64.zip", BrowserDownloadURL: "https://example.com/fnm-darwin-amd64.zip", Digest: "sha256:" + testHash1},
			{Name: "fnm-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/fnm-darwin-arm64.zip", Digest: "sha256:" + testHash2},
			{Name: "fnm-linux-amd64.zip", BrowserDownloadURL: "https://example.com/fnm-linux-amd64.zip", Digest: "sha256:" + testHash3},
			{Name: "fnm-linux-arm64.zip", BrowserDownloadURL: "https://example.com/fnm-linux-arm64.zip", Digest: "sha256:" + testHash4},
		},
	}
	fnmBinaries, err := detectRuntimeBinaries("fnm", fnmRelease)
	if err != nil {
		t.Fatalf("detectRuntimeBinaries(fnm): %v", err)
	}
	fnmData := &FNMRuntimeData{NodeVersion: "24.14.0", PNPMVersion: "10.31.0", PNPMHash: testHash5}
	fnmJSON := buildFNMRuntimeJSON(fnmData, fnmBinaries)

	// Simulate UV release (separate gnu + musl → both keys)
	uvRelease := &github.Release{
		TagName: "0.10.9",
		Assets: []github.Asset{
			{Name: "uv-x86_64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/uv-darwin-amd64", Digest: "sha256:" + testHash1},
			{Name: "uv-x86_64-unknown-linux-gnu.tar.gz", BrowserDownloadURL: "https://example.com/uv-linux-gnu", Digest: "sha256:" + testHash2},
			{Name: "uv-x86_64-unknown-linux-musl.tar.gz", BrowserDownloadURL: "https://example.com/uv-linux-musl", Digest: "sha256:" + testHash3},
		},
	}
	uvBinaries, err := detectRuntimeBinaries("uv", uvRelease)
	if err != nil {
		t.Fatalf("detectRuntimeBinaries(uv): %v", err)
	}
	uvData := &UVRuntimeData{PythonVersion: "3.14.3"}
	uvJSON := buildUVRuntimeJSON(uvData, uvBinaries)

	// Simulate JVM release (linux + alpine-linux → both keys)
	jvmRelease := &github.Release{
		TagName: "jdk-25.0.2+10",
		Assets: []github.Asset{
			{Name: "OpenJDK25U-jdk_x64_mac_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/jdk-mac", Digest: "sha256:" + testHash1},
			{Name: "OpenJDK25U-jdk_x64_linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/jdk-linux", Digest: "sha256:" + testHash2},
			{Name: "OpenJDK25U-jdk_x64_alpine-linux_hotspot_25.0.2_10.tar.gz", BrowserDownloadURL: "https://example.com/jdk-alpine", Digest: "sha256:" + testHash3},
		},
	}
	jvmBinaries, err := detectJVMBinaries(jvmRelease)
	if err != nil {
		t.Fatalf("detectJVMBinaries: %v", err)
	}
	jvmData := &JVMRuntimeData{JavaVersion: "25"}
	jvmJSON := buildJVMRuntimeJSON(jvmData, jvmBinaries)

	// Assemble and write
	runtimes := RuntimesJSON{
		"fnm": fnmJSON,
		"uv":  uvJSON,
		"jvm": jvmJSON,
	}

	if err := writeRuntimesJSON(outputPath, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON: %v", err)
	}

	// Read back
	read, err := readRuntimesJSON(outputPath)
	if err != nil {
		t.Fatalf("readRuntimesJSON: %v", err)
	}

	// Verify all three runtimes present
	for _, key := range []string{"fnm", "uv", "jvm"} {
		if _, ok := read[key]; !ok {
			t.Errorf("missing runtime %q after roundtrip", key)
		}
	}

	// Verify kinds
	if read["fnm"].Kind != "fnm" {
		t.Errorf("fnm kind = %q", read["fnm"].Kind)
	}
	if read["uv"].Kind != "uv" {
		t.Errorf("uv kind = %q", read["uv"].Kind)
	}
	if read["jvm"].Kind != "jvm" {
		t.Errorf("jvm kind = %q", read["jvm"].Kind)
	}

	// Verify config sections
	if read["fnm"].FNM == nil || read["fnm"].FNM.NodeVersion != "24.14.0" {
		t.Error("fnm config missing or wrong NodeVersion")
	}
	if read["uv"].UV == nil || read["uv"].UV.PythonVersion != "3.14.3" {
		t.Error("uv config missing or wrong PythonVersion")
	}
	if read["jvm"].JVM == nil || read["jvm"].JVM.JavaVersion != "25" {
		t.Error("jvm config missing or wrong JavaVersion")
	}

	// Verify all runtimes have managed binaries
	for _, key := range []string{"fnm", "uv", "jvm"} {
		if read[key].Managed == nil || len(read[key].Managed.Binaries) == 0 {
			t.Errorf("%s: missing managed binaries", key)
		}
	}
}

func TestIntegration_MuslBinaryAddition(t *testing.T) {
	// Verify that when upstream provides separate musl binaries (UV, JVM),
	// both glibc and musl entries appear in the final JSON output.
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "runtimes.json")

	// UV with separate gnu and musl for both amd64 and arm64
	uvRelease := &github.Release{
		TagName: "0.10.9",
		Assets: []github.Asset{
			{Name: "uv-x86_64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/uv-darwin-amd64", Digest: "sha256:" + testHash1},
			{Name: "uv-aarch64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/uv-darwin-arm64", Digest: "sha256:" + testHash2},
			{Name: "uv-x86_64-unknown-linux-gnu.tar.gz", BrowserDownloadURL: "https://example.com/uv-gnu-amd64", Digest: "sha256:" + testHash3},
			{Name: "uv-x86_64-unknown-linux-musl.tar.gz", BrowserDownloadURL: "https://example.com/uv-musl-amd64", Digest: "sha256:" + testHash4},
			{Name: "uv-aarch64-unknown-linux-gnu.tar.gz", BrowserDownloadURL: "https://example.com/uv-gnu-arm64", Digest: "sha256:" + testHash5},
		},
	}
	uvBinaries, err := detectRuntimeBinaries("uv", uvRelease)
	if err != nil {
		t.Fatalf("detectRuntimeBinaries(uv): %v", err)
	}

	uvJSON := buildUVRuntimeJSON(&UVRuntimeData{PythonVersion: "3.14.3"}, uvBinaries)
	runtimes := RuntimesJSON{"uv": uvJSON}

	if err := writeRuntimesJSON(outputPath, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON: %v", err)
	}

	read, err := readRuntimesJSON(outputPath)
	if err != nil {
		t.Fatalf("readRuntimesJSON: %v", err)
	}

	uv := read["uv"]

	// linux/amd64 should have BOTH glibc and musl
	linuxAmd64 := uv.Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("uv linux/amd64: missing glibc entry")
	}
	if _, ok := linuxAmd64["musl"]; !ok {
		t.Error("uv linux/amd64: missing musl entry")
	}

	// Verify different URLs
	if linuxAmd64["glibc"].URL == linuxAmd64["musl"].URL {
		t.Error("uv linux/amd64: glibc and musl should have different URLs")
	}

	// darwin should use "unknown" (no musl/glibc on macOS)
	darwinAmd64 := uv.Managed.Binaries[syslist.OsTypeDarwin][syslist.ArchTypeAmd64]
	if _, ok := darwinAmd64["unknown"]; !ok {
		t.Error("uv darwin/amd64: missing 'unknown' libc entry")
	}
}

func TestIntegration_GracefulFallbackNoMusl(t *testing.T) {
	// When upstream has a single Linux binary (no musl indicator), only glibc
	// key should be created. The resolver's fallback handles musl at runtime.
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "runtimes.json")

	// FNM: single Linux binary per arch (no musl variant)
	fnmRelease := &github.Release{
		TagName: "v1.40.0",
		Assets: []github.Asset{
			{Name: "fnm-darwin-amd64.zip", BrowserDownloadURL: "https://example.com/fnm-darwin-amd64.zip", Digest: "sha256:" + testHash1},
			{Name: "fnm-linux-amd64.zip", BrowserDownloadURL: "https://example.com/fnm-linux-amd64.zip", Digest: "sha256:" + testHash2},
			{Name: "fnm-linux-arm64.zip", BrowserDownloadURL: "https://example.com/fnm-linux-arm64.zip", Digest: "sha256:" + testHash3},
		},
	}
	fnmBinaries, err := detectRuntimeBinaries("fnm", fnmRelease)
	if err != nil {
		t.Fatalf("detectRuntimeBinaries(fnm): %v", err)
	}

	fnmJSON := buildFNMRuntimeJSON(
		&FNMRuntimeData{NodeVersion: "24.14.0", PNPMVersion: "10.31.0", PNPMHash: testHash4},
		fnmBinaries,
	)
	runtimes := RuntimesJSON{"fnm": fnmJSON}

	if err := writeRuntimesJSON(outputPath, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON: %v", err)
	}

	read, err := readRuntimesJSON(outputPath)
	if err != nil {
		t.Fatalf("readRuntimesJSON: %v", err)
	}

	fnm := read["fnm"]
	linuxBins := fnm.Managed.Binaries[syslist.OsTypeLinux]

	// Check all Linux arch entries: should only have glibc, never musl
	for arch, libcMap := range linuxBins {
		if _, ok := libcMap["glibc"]; !ok {
			t.Errorf("fnm linux/%s: expected glibc entry", arch)
		}
		if _, ok := libcMap["musl"]; ok {
			t.Errorf("fnm linux/%s: musl entry should NOT exist (single binary, deduplicated)", arch)
		}
	}
}

func TestIntegration_VersionDetectionWithFallback(t *testing.T) {
	// Test that runtimeVersion() correctly extracts version info from
	// the built JSON structures, and that version strings appear in output.

	fnmJSON := buildFNMRuntimeJSON(
		&FNMRuntimeData{NodeVersion: "24.14.0", PNPMVersion: "10.31.0", PNPMHash: testHash1},
		binmanager.MapOfBinaries{
			syslist.OsTypeDarwin: {
				syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{URL: "x", Hash: testHash1}},
			},
		},
	)

	uvJSON := buildUVRuntimeJSON(
		&UVRuntimeData{PythonVersion: "3.14.3"},
		binmanager.MapOfBinaries{
			syslist.OsTypeDarwin: {
				syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{URL: "x", Hash: testHash1}},
			},
		},
	)

	jvmJSON := buildJVMRuntimeJSON(
		&JVMRuntimeData{JavaVersion: "25"},
		binmanager.MapOfBinaries{
			syslist.OsTypeDarwin: {
				syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{URL: "x", Hash: testHash1}},
			},
		},
	)

	// FNM: should include node and pnpm versions
	fnmVer := runtimeVersion(fnmJSON)
	if !strings.Contains(fnmVer, "node=24.14.0") {
		t.Errorf("FNM version should contain node version, got %q", fnmVer)
	}
	if !strings.Contains(fnmVer, "pnpm=10.31.0") {
		t.Errorf("FNM version should contain pnpm version, got %q", fnmVer)
	}

	// UV: should include python version
	uvVer := runtimeVersion(uvJSON)
	if !strings.Contains(uvVer, "python=3.14.3") {
		t.Errorf("UV version should contain python version, got %q", uvVer)
	}

	// JVM: should include java version
	jvmVer := runtimeVersion(jvmJSON)
	if !strings.Contains(jvmVer, "java=25") {
		t.Errorf("JVM version should contain java version, got %q", jvmVer)
	}

	// Nil runtime returns empty
	if v := runtimeVersion(nil); v != "" {
		t.Errorf("nil runtime version should be empty, got %q", v)
	}

	// Write and read back, verify versions survive roundtrip
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	runtimes := RuntimesJSON{"fnm": fnmJSON, "uv": uvJSON, "jvm": jvmJSON}

	if err := writeRuntimesJSON(path, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON: %v", err)
	}
	read, err := readRuntimesJSON(path)
	if err != nil {
		t.Fatalf("readRuntimesJSON: %v", err)
	}

	readFNMVer := runtimeVersion(read["fnm"])
	if readFNMVer != fnmVer {
		t.Errorf("FNM version mismatch after roundtrip: %q vs %q", readFNMVer, fnmVer)
	}
	readUVVer := runtimeVersion(read["uv"])
	if readUVVer != uvVer {
		t.Errorf("UV version mismatch after roundtrip: %q vs %q", readUVVer, uvVer)
	}
	readJVMVer := runtimeVersion(read["jvm"])
	if readJVMVer != jvmVer {
		t.Errorf("JVM version mismatch after roundtrip: %q vs %q", readJVMVer, jvmVer)
	}
}

func TestIntegration_JSONGeneration(t *testing.T) {
	// Verify the generated JSON matches expected structure for config import.
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "runtimes.json")

	runtimes := buildTestRuntimes()
	if err := writeRuntimesJSON(outputPath, runtimes); err != nil {
		t.Fatalf("writeRuntimesJSON: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)

	// Must be valid JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Must have exactly three top-level keys
	if len(raw) != 3 {
		t.Errorf("expected 3 top-level keys, got %d", len(raw))
	}

	// Each runtime must have required fields
	for _, name := range []string{"fnm", "uv", "jvm"} {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw[name], &entry); err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		requiredFields := []string{"kind", "mode", "managed"}
		for _, field := range requiredFields {
			if _, ok := entry[field]; !ok {
				t.Errorf("%s: missing required field %q", name, field)
			}
		}

		// Verify kind matches key name
		var kind string
		if err := json.Unmarshal(entry["kind"], &kind); err != nil {
			t.Fatalf("parsing %s kind: %v", name, err)
		}
		if kind != name {
			t.Errorf("%s: kind = %q, want %q", name, kind, name)
		}

		// Verify mode is "managed"
		var mode string
		if err := json.Unmarshal(entry["mode"], &mode); err != nil {
			t.Fatalf("parsing %s mode: %v", name, err)
		}
		if mode != "managed" {
			t.Errorf("%s: mode = %q, want %q", name, mode, "managed")
		}
	}

	// FNM must have fnm config section with nodeVersion, pnpmVersion, pnpmHash
	var fnmEntry map[string]json.RawMessage
	_ = json.Unmarshal(raw["fnm"], &fnmEntry)
	var fnmConfig FNMConfigJSON
	if err := json.Unmarshal(fnmEntry["fnm"], &fnmConfig); err != nil {
		t.Fatalf("parsing fnm config: %v", err)
	}
	if fnmConfig.NodeVersion == "" {
		t.Error("fnm: nodeVersion is empty")
	}
	if fnmConfig.PNPMVersion == "" {
		t.Error("fnm: pnpmVersion is empty")
	}
	if fnmConfig.PNPMHash == "" {
		t.Error("fnm: pnpmHash is empty")
	}

	// UV must have uv config section with pythonVersion
	var uvEntry map[string]json.RawMessage
	_ = json.Unmarshal(raw["uv"], &uvEntry)
	var uvConfig UVConfigJSON
	if err := json.Unmarshal(uvEntry["uv"], &uvConfig); err != nil {
		t.Fatalf("parsing uv config: %v", err)
	}
	if uvConfig.PythonVersion == "" {
		t.Error("uv: pythonVersion is empty")
	}

	// JVM must have jvm config section with javaVersion
	var jvmEntry map[string]json.RawMessage
	_ = json.Unmarshal(raw["jvm"], &jvmEntry)
	var jvmConfig JVMConfigJSON
	if err := json.Unmarshal(jvmEntry["jvm"], &jvmConfig); err != nil {
		t.Fatalf("parsing jvm config: %v", err)
	}
	if jvmConfig.JavaVersion == "" {
		t.Error("jvm: javaVersion is empty")
	}

	// Verify formatting: 2-space indentation, trailing newline
	if !strings.HasSuffix(content, "\n") {
		t.Error("JSON output should end with trailing newline")
	}
	if strings.Contains(content, "\t") {
		t.Error("JSON output should not contain tabs")
	}

	// Verify managed section has binaries with nested structure
	read, err := readRuntimesJSON(outputPath)
	if err != nil {
		t.Fatalf("readRuntimesJSON: %v", err)
	}

	// UV linux should have both glibc and musl (from buildTestRuntimes)
	uvLinux := read["uv"].Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := uvLinux["glibc"]; !ok {
		t.Error("UV JSON: missing linux/amd64/glibc")
	}
	if _, ok := uvLinux["musl"]; !ok {
		t.Error("UV JSON: missing linux/amd64/musl")
	}

	// JVM linux should have both glibc and musl
	jvmLinux := read["jvm"].Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := jvmLinux["glibc"]; !ok {
		t.Error("JVM JSON: missing linux/amd64/glibc")
	}
	if _, ok := jvmLinux["musl"]; !ok {
		t.Error("JVM JSON: missing linux/amd64/musl")
	}

	// FNM linux should have only glibc (no musl in buildTestRuntimes)
	fnmLinux := read["fnm"].Managed.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := fnmLinux["glibc"]; !ok {
		t.Error("FNM JSON: missing linux/amd64/glibc")
	}
	if _, ok := fnmLinux["musl"]; ok {
		t.Error("FNM JSON: unexpected linux/amd64/musl")
	}

	// JVM entries should have ExtractDir=true
	for libc, info := range jvmLinux {
		if !info.ExtractDir {
			t.Errorf("JVM linux/amd64/%s: ExtractDir should be true", libc)
		}
	}
}
