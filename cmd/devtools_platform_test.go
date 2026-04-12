package cmd

import (
	"github.com/datamitsu/datamitsu/internal/appstate"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"testing"
)

// valid 64-char hex hashes for test assets
const (
	testHash1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testHash2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testHash3 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testHash4 = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	testHash5 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func TestBuildPlatformTuples_ContainsLinuxLibcVariants(t *testing.T) {
	tuples := buildPlatformTuples()

	type key struct {
		os   syslist.OsType
		arch syslist.ArchType
		libc string
	}

	expected := []key{
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "glibc"},
		{syslist.OsTypeLinux, syslist.ArchTypeAmd64, "musl"},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "glibc"},
		{syslist.OsTypeLinux, syslist.ArchTypeArm64, "musl"},
	}

	found := make(map[key]bool)
	for _, t := range tuples {
		found[key(t)] = true
	}

	for _, e := range expected {
		if !found[e] {
			t.Errorf("missing expected tuple: %s/%s/%s", e.os, e.arch, e.libc)
		}
	}
}

func TestBuildPlatformTuples_NonLinuxUsesUnknown(t *testing.T) {
	tuples := buildPlatformTuples()

	for _, tuple := range tuples {
		if tuple.os == syslist.OsTypeLinux {
			if tuple.libc != "glibc" && tuple.libc != "musl" {
				t.Errorf("Linux tuple has unexpected libc %q", tuple.libc)
			}
		} else {
			if tuple.libc != "unknown" {
				t.Errorf("%s tuple has libc %q, want \"unknown\"", tuple.os, tuple.libc)
			}
		}
	}
}

func TestBuildPlatformTuples_Count(t *testing.T) {
	tuples := buildPlatformTuples()

	// 4 non-Linux OS * 2 arches = 8 "unknown" tuples
	// 1 Linux * 2 arches * 2 libcs = 4 Linux tuples
	// Total = 12
	if len(tuples) != 12 {
		t.Errorf("expected 12 tuples, got %d", len(tuples))
	}
}

func TestBuildPlatformTuples_AllHaveLibc(t *testing.T) {
	tuples := buildPlatformTuples()
	for _, tuple := range tuples {
		if tuple.libc == "" {
			t.Errorf("tuple %s/%s has empty libc", tuple.os, tuple.arch)
		}
	}
}

func TestBuildBinariesForApp_NestedStorageStructure(t *testing.T) {
	assets := []github.Asset{
		{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/tool-linux-amd64.tar.gz", Digest: "sha256:" + testHash1},
		{Name: "tool-linux-musl-amd64.tar.gz", BrowserDownloadURL: "https://example.com/tool-linux-musl-amd64.tar.gz", Digest: "sha256:" + testHash2},
		{Name: "tool-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/tool-darwin-arm64.tar.gz", Digest: "sha256:" + testHash3},
		{Name: "tool-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/tool-darwin-amd64.tar.gz", Digest: "sha256:" + testHash4},
		{Name: "tool-linux-arm64.tar.gz", BrowserDownloadURL: "https://example.com/tool-linux-arm64.tar.gz", Digest: "sha256:" + testHash5},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash123", state)
	if err != nil {
		t.Fatalf("buildBinariesForApp failed: %v", err)
	}

	if entry == nil {
		t.Fatal("expected binaries entry for 'tool'")
	}

	// Verify nested structure: linux/amd64 should have both glibc and musl entries
	linuxBins := entry.Binaries[syslist.OsTypeLinux]
	if linuxBins == nil {
		t.Fatal("expected linux binaries")
	}

	amd64Bins := linuxBins[syslist.ArchTypeAmd64]
	if amd64Bins == nil {
		t.Fatal("expected linux/amd64 binaries")
	}

	if _, ok := amd64Bins["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}
	if _, ok := amd64Bins["musl"]; !ok {
		t.Error("expected linux/amd64/musl entry")
	}

	// Verify musl asset was selected for musl tuple
	muslInfo := amd64Bins["musl"]
	if muslInfo.URL != "https://example.com/tool-linux-musl-amd64.tar.gz" {
		t.Errorf("musl entry URL = %q, want musl asset URL", muslInfo.URL)
	}

	// Verify darwin uses "unknown" libc key
	darwinBins := entry.Binaries[syslist.OsTypeDarwin]
	if darwinBins == nil {
		t.Fatal("expected darwin binaries")
	}
	arm64Bins := darwinBins[syslist.ArchTypeArm64]
	if arm64Bins == nil {
		t.Fatal("expected darwin/arm64 binaries")
	}
	if _, ok := arm64Bins["unknown"]; !ok {
		t.Error("expected darwin/arm64/unknown entry")
	}
}

func TestBuildBinariesForApp_DetectorCalledWithLibcType(t *testing.T) {
	// When both musl and glibc assets exist, the detector should pick the
	// correct one for each libc tuple
	assets := []github.Asset{
		{Name: "app-linux-gnu-amd64.tar.gz", BrowserDownloadURL: "https://example.com/gnu", Digest: "sha256:" + testHash1},
		{Name: "app-linux-musl-amd64.tar.gz", BrowserDownloadURL: "https://example.com/musl", Digest: "sha256:" + testHash2},
		{Name: "app-linux-gnu-arm64.tar.gz", BrowserDownloadURL: "https://example.com/gnu-arm", Digest: "sha256:" + testHash3},
	}

	release := &github.Release{
		TagName: "v2.0.0",
		Assets:  assets,
	}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("app", release, "hash456", state)
	if err != nil {
		t.Fatalf("buildBinariesForApp failed: %v", err)
	}

	linuxAmd64 := entry.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]

	// glibc tuple should select the gnu asset
	glibcInfo := linuxAmd64["glibc"]
	if glibcInfo.URL != "https://example.com/gnu" {
		t.Errorf("glibc entry URL = %q, want gnu asset", glibcInfo.URL)
	}

	// musl tuple should select the musl asset
	muslInfo := linuxAmd64["musl"]
	if muslInfo.URL != "https://example.com/musl" {
		t.Errorf("musl entry URL = %q, want musl asset", muslInfo.URL)
	}
}

func TestBuildBinariesForApp_InitializesNestedMaps(t *testing.T) {
	assets := []github.Asset{
		{Name: "tool-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash789", state)
	if err != nil {
		t.Fatalf("buildBinariesForApp failed: %v", err)
	}

	// Verify the nested maps were properly initialized (no nil map panics)
	if entry == nil {
		t.Fatal("expected binaries entry")
	}

	darwinBins := entry.Binaries[syslist.OsTypeDarwin]
	if darwinBins == nil {
		t.Fatal("expected darwin map to be initialized")
	}

	arm64Bins := darwinBins[syslist.ArchTypeArm64]
	if arm64Bins == nil {
		t.Fatal("expected darwin/arm64 map to be initialized")
	}

	info, ok := arm64Bins["unknown"]
	if !ok {
		t.Fatal("expected darwin/arm64/unknown entry")
	}
	if info.URL != "https://example.com/darwin" {
		t.Errorf("URL = %q, want darwin URL", info.URL)
	}
}

func TestFormatPlatformLabel(t *testing.T) {
	tests := []struct {
		name string
		r    detectionResult
		want string
	}{
		{
			"linux with glibc",
			detectionResult{os: syslist.OsTypeLinux, arch: syslist.ArchTypeAmd64, libc: "glibc"},
			"linux/amd64/glibc",
		},
		{
			"linux with musl",
			detectionResult{os: syslist.OsTypeLinux, arch: syslist.ArchTypeArm64, libc: "musl"},
			"linux/arm64/musl",
		},
		{
			"darwin with unknown",
			detectionResult{os: syslist.OsTypeDarwin, arch: syslist.ArchTypeArm64, libc: "unknown"},
			"darwin/arm64",
		},
		{
			"empty libc",
			detectionResult{os: syslist.OsTypeWindows, arch: syslist.ArchTypeAmd64, libc: ""},
			"windows/amd64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatPlatformLabel(tt.r)
			if got != tt.want {
				t.Errorf("formatPlatformLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildBinariesForApp_MapsExistBeforeWrite(t *testing.T) {
	// Verify that maps are properly initialized even when state starts nil
	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	assets := []github.Asset{
		{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/1", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{
		TagName: "v1.0.0",
		Assets:  assets,
	}

	entry, err := buildBinariesForApp("mytool", release, "hash", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All three levels of the map should exist
	if entry == nil {
		t.Fatal("binaries entry is nil")
	}
	if entry.Binaries == nil {
		t.Fatal("MapOfBinaries is nil")
	}

	osMap := entry.Binaries[syslist.OsTypeLinux]
	if osMap == nil {
		t.Fatal("OS map is nil")
	}

	archMap := osMap[syslist.ArchTypeAmd64]
	if archMap == nil {
		t.Fatal("arch map is nil")
	}

	// Only glibc should exist — musl is deduplicated when same binary detected
	if _, ok := archMap["glibc"]; !ok {
		t.Error("missing glibc entry")
	}
	if _, ok := archMap["musl"]; ok {
		t.Error("musl entry should not exist when same binary as glibc (deduplicated)")
	}
}

func TestBuildBinariesForApp_ConfigHash(t *testing.T) {
	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	assets := []github.Asset{
		{Name: "tool-darwin-amd64", BrowserDownloadURL: "https://example.com/bin", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{TagName: "v1.0.0", Assets: assets}

	entry, err := buildBinariesForApp("tool", release, "myhash", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.ConfigHash != "myhash" {
		t.Errorf("ConfigHash = %q, want %q", entry.ConfigHash, "myhash")
	}
}

func TestBuildBinariesForApp_CorrectContentType(t *testing.T) {
	assets := []github.Asset{
		{Name: "tool-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/zip", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{TagName: "v1.0.0", Assets: assets}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info := entry.Binaries[syslist.OsTypeDarwin][syslist.ArchTypeArm64]["unknown"]
	if info.ContentType != binmanager.BinContentTypeZip {
		t.Errorf("ContentType = %v, want zip", info.ContentType)
	}
}

func TestBuildBinariesForApp_RejectsLibcMismatch(t *testing.T) {
	// If only musl assets exist, glibc tuple should NOT get the musl asset
	assets := []github.Asset{
		{Name: "tool-linux-musl-amd64.tar.gz", BrowserDownloadURL: "https://example.com/musl", Digest: "sha256:" + testHash1},
		{Name: "tool-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin", Digest: "sha256:" + testHash2},
	}

	release := &github.Release{TagName: "v1.0.0", Assets: assets}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// musl tuple should have the asset
	linuxBins := entry.Binaries[syslist.OsTypeLinux]
	if linuxBins != nil {
		amd64Bins := linuxBins[syslist.ArchTypeAmd64]
		if amd64Bins != nil {
			if _, ok := amd64Bins["musl"]; !ok {
				t.Error("expected linux/amd64/musl entry")
			}
			// glibc tuple should NOT have the musl asset
			if _, ok := amd64Bins["glibc"]; ok {
				t.Error("glibc entry should not exist when only musl assets available")
			}
		}
	}
}

func TestBuildBinariesForApp_DoesNotMutateStateOnFailure(t *testing.T) {
	// No matching assets -> should return error without mutating state
	assets := []github.Asset{
		{Name: "something-completely-unrelated.txt", BrowserDownloadURL: "https://example.com/txt", Digest: "sha256:" + testHash1},
	}

	release := &github.Release{TagName: "v1.0.0", Assets: assets}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash", state)
	if err == nil {
		t.Fatal("expected error for no matching binaries")
	}
	if entry != nil {
		t.Error("expected nil entry on failure")
	}

	// State should be unchanged
	if _, ok := state.Binaries["tool"]; ok {
		t.Error("state.Binaries should not be mutated on failure")
	}
}

func TestBuildBinariesForApp_DeduplicatesSingleLinuxBinary(t *testing.T) {
	// When a single Linux binary has no libc indicator, it gets detected for both
	// glibc and musl. Deduplication should keep only the glibc entry.
	assets := []github.Asset{
		{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64", Digest: "sha256:" + testHash1},
		{Name: "tool-linux-arm64.tar.gz", BrowserDownloadURL: "https://example.com/linux-arm64", Digest: "sha256:" + testHash2},
		{Name: "tool-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-amd64", Digest: "sha256:" + testHash3},
	}

	release := &github.Release{TagName: "v1.0.0", Assets: assets}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// linux/amd64 should only have glibc (musl deduplicated)
	linuxAmd64 := entry.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}
	if _, ok := linuxAmd64["musl"]; ok {
		t.Error("linux/amd64/musl should be deduplicated (same binary as glibc)")
	}

	// linux/arm64 should only have glibc (musl deduplicated)
	linuxArm64 := entry.Binaries[syslist.OsTypeLinux][syslist.ArchTypeArm64]
	if _, ok := linuxArm64["glibc"]; !ok {
		t.Error("expected linux/arm64/glibc entry")
	}
	if _, ok := linuxArm64["musl"]; ok {
		t.Error("linux/arm64/musl should be deduplicated (same binary as glibc)")
	}
}

func TestBuildBinariesForApp_SeparateMuslBinariesNotDeduplicated(t *testing.T) {
	// When separate musl binaries exist with different URLs/hashes,
	// both glibc and musl entries should be created.
	assets := []github.Asset{
		{Name: "tool-linux-gnu-amd64.tar.gz", BrowserDownloadURL: "https://example.com/gnu-amd64", Digest: "sha256:" + testHash1},
		{Name: "tool-linux-musl-amd64.tar.gz", BrowserDownloadURL: "https://example.com/musl-amd64", Digest: "sha256:" + testHash2},
		{Name: "tool-linux-gnu-arm64.tar.gz", BrowserDownloadURL: "https://example.com/gnu-arm64", Digest: "sha256:" + testHash3},
		{Name: "tool-linux-musl-arm64.tar.gz", BrowserDownloadURL: "https://example.com/musl-arm64", Digest: "sha256:" + testHash4},
	}

	release := &github.Release{TagName: "v1.0.0", Assets: assets}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both glibc and musl should exist for amd64
	linuxAmd64 := entry.Binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if _, ok := linuxAmd64["glibc"]; !ok {
		t.Error("expected linux/amd64/glibc entry")
	}
	if _, ok := linuxAmd64["musl"]; !ok {
		t.Error("expected linux/amd64/musl entry (separate binary)")
	}

	// Both glibc and musl should exist for arm64
	linuxArm64 := entry.Binaries[syslist.OsTypeLinux][syslist.ArchTypeArm64]
	if _, ok := linuxArm64["glibc"]; !ok {
		t.Error("expected linux/arm64/glibc entry")
	}
	if _, ok := linuxArm64["musl"]; !ok {
		t.Error("expected linux/arm64/musl entry (separate binary)")
	}

	// Verify different URLs for glibc vs musl
	if linuxAmd64["glibc"].URL == linuxAmd64["musl"].URL {
		t.Error("glibc and musl should have different URLs")
	}
}

func TestBuildBinariesForApp_NoDuplicateURLHashPairs(t *testing.T) {
	// After deduplication, no two entries for the same OS/arch should have
	// the same URL+hash combination.
	assets := []github.Asset{
		{Name: "tool-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux", Digest: "sha256:" + testHash1},
		{Name: "tool-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin", Digest: "sha256:" + testHash2},
	}

	release := &github.Release{TagName: "v1.0.0", Assets: assets}

	state := &appstate.State{
		Apps:     map[string]*appstate.AppMetadata{},
		Binaries: map[string]*appstate.BinariesEntry{},
	}

	entry, err := buildBinariesForApp("tool", release, "hash", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect all URL+hash pairs per OS/arch
	type urlHash struct {
		url  string
		hash string
	}
	seen := make(map[string][]urlHash) // key: "os/arch"
	for osType, archMap := range entry.Binaries {
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

func TestExtractHashFromDigest_ValidHash(t *testing.T) {
	hash, err := extractHashFromDigest("sha256:" + testHash1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != testHash1 {
		t.Errorf("hash = %q, want %q", hash, testHash1)
	}
}

func TestExtractHashFromDigest_InvalidHex(t *testing.T) {
	_, err := extractHashFromDigest("sha256:not-a-valid-hex-string-of-the-right-length-for-sha256-000000")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestExtractHashFromDigest_WrongLength(t *testing.T) {
	_, err := extractHashFromDigest("sha256:abc123")
	if err == nil {
		t.Error("expected error for wrong length")
	}
}

func TestExtractHashFromDigest_EmptyDigest(t *testing.T) {
	_, err := extractHashFromDigest("")
	if err == nil {
		t.Error("expected error for empty digest")
	}
}

func TestExtractHashFromDigest_UnsupportedAlgorithm(t *testing.T) {
	_, err := extractHashFromDigest("md5:abc123")
	if err == nil {
		t.Error("expected error for unsupported algorithm")
	}
}
