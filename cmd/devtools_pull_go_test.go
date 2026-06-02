package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

const goTestVersion = "1.26.3"

// buildGoTestFiles assigns a distinct, valid SHA-256-shaped hash to every Go
// archive tuple for version v, mimicking the filename→sha256 map go.dev returns.
func buildGoTestFiles(v string) map[string]string {
	files := map[string]string{}
	const hexChars = "0123456789abcdef"
	for i, spec := range goArchiveSpecs(v) {
		files[spec.filename] = strings.Repeat(string(hexChars[i]), 64)
	}
	return files
}

func TestGoArchiveSpecs_FilenamesAndPaths(t *testing.T) {
	specs := goArchiveSpecs(goTestVersion)
	if len(specs) != 6 {
		t.Fatalf("expected 6 specs, got %d", len(specs))
	}

	type want struct {
		filename    string
		binaryPath  string
		contentType binmanager.BinContentType
	}
	wants := map[string]want{
		"darwin/amd64/unknown":  {"go1.26.3.darwin-amd64.tar.gz", "go/bin/go", binmanager.BinContentTypeTarGz},
		"darwin/arm64/unknown":  {"go1.26.3.darwin-arm64.tar.gz", "go/bin/go", binmanager.BinContentTypeTarGz},
		"linux/amd64/glibc":     {"go1.26.3.linux-amd64.tar.gz", "go/bin/go", binmanager.BinContentTypeTarGz},
		"linux/arm64/glibc":     {"go1.26.3.linux-arm64.tar.gz", "go/bin/go", binmanager.BinContentTypeTarGz},
		"windows/amd64/unknown": {"go1.26.3.windows-amd64.zip", "go/bin/go.exe", binmanager.BinContentTypeZip},
		"windows/arm64/unknown": {"go1.26.3.windows-arm64.zip", "go/bin/go.exe", binmanager.BinContentTypeZip},
	}

	for _, spec := range specs {
		key := fmt.Sprintf("%s/%s/%s", spec.os, spec.arch, spec.libc)
		w, ok := wants[key]
		if !ok {
			t.Errorf("unexpected spec tuple: %s", key)
			continue
		}
		if spec.filename != w.filename {
			t.Errorf("%s: filename = %q, want %q", key, spec.filename, w.filename)
		}
		if got := goBinaryPath(spec); got != w.binaryPath {
			t.Errorf("%s: binaryPath = %q, want %q", key, got, w.binaryPath)
		}
		if spec.contentType != w.contentType {
			t.Errorf("%s: contentType = %q, want %q", key, spec.contentType, w.contentType)
		}
		delete(wants, key)
	}
	if len(wants) != 0 {
		t.Errorf("specs missing tuples: %v", wants)
	}
}

func TestGoBinaryPath(t *testing.T) {
	gz := goArchiveSpec{filename: "go1.26.3.linux-amd64.tar.gz", contentType: binmanager.BinContentTypeTarGz}
	if got := goBinaryPath(gz); got != "go/bin/go" {
		t.Errorf("tar.gz binaryPath = %q", got)
	}
	zip := goArchiveSpec{filename: "go1.26.3.windows-amd64.zip", contentType: binmanager.BinContentTypeZip}
	if got := goBinaryPath(zip); got != "go/bin/go.exe" {
		t.Errorf("zip binaryPath = %q", got)
	}
}

func TestBuildGoBinaries_AllTuples(t *testing.T) {
	files := buildGoTestFiles(goTestVersion)

	binaries, err := buildGoBinaries(goPullConfig{
		version: goTestVersion,
		baseURL: goDistBaseURL,
		files:   files,
	})
	if err != nil {
		t.Fatalf("buildGoBinaries: %v", err)
	}

	for _, spec := range goArchiveSpecs(goTestVersion) {
		info, ok := binaries[spec.os][spec.arch][spec.libc]
		if !ok {
			t.Errorf("%s/%s/%s: missing entry", spec.os, spec.arch, spec.libc)
			continue
		}
		wantURL := fmt.Sprintf("%s/%s", goDistBaseURL, spec.filename)
		if info.URL != wantURL {
			t.Errorf("%s: URL = %q, want %q", spec.filename, info.URL, wantURL)
		}
		if info.Hash != files[spec.filename] {
			t.Errorf("%s: Hash = %q, want %q", spec.filename, info.Hash, files[spec.filename])
		}
		if info.ContentType != spec.contentType {
			t.Errorf("%s: ContentType = %q, want %q", spec.filename, info.ContentType, spec.contentType)
		}
		if info.BinaryPath == nil || *info.BinaryPath != goBinaryPath(spec) {
			t.Errorf("%s: BinaryPath = %v, want %q", spec.filename, info.BinaryPath, goBinaryPath(spec))
		}
		if !info.ExtractDir {
			t.Errorf("%s: ExtractDir should be true", spec.filename)
		}
	}
}

func TestBuildGoBinaries_MissingHash(t *testing.T) {
	files := buildGoTestFiles(goTestVersion)
	dropped := "go" + goTestVersion + ".linux-amd64.tar.gz"
	delete(files, dropped)

	_, err := buildGoBinaries(goPullConfig{version: goTestVersion, baseURL: goDistBaseURL, files: files})
	if err == nil {
		t.Fatal("expected hard error when a required archive hash is absent (no hash, no entry)")
	}
	if !strings.Contains(err.Error(), dropped) {
		t.Errorf("error should name the missing archive %q, got: %v", dropped, err)
	}
}

func TestBuildGoBinaries_InvalidHash(t *testing.T) {
	files := buildGoTestFiles(goTestVersion)
	files["go"+goTestVersion+".linux-amd64.tar.gz"] = "not-a-valid-sha256"

	if _, err := buildGoBinaries(goPullConfig{version: goTestVersion, baseURL: goDistBaseURL, files: files}); err == nil {
		t.Fatal("expected error for malformed SHA-256 hash")
	}
}

// TestBuildGoBinaries_InvalidNonHexHashRejected ensures normalization does not
// paper over a genuinely malformed hash: a 64-char non-hex value must still
// hard-error after lowercasing.
func TestBuildGoBinaries_InvalidNonHexHashRejected(t *testing.T) {
	files := buildGoTestFiles(goTestVersion)
	files["go"+goTestVersion+".linux-amd64.tar.gz"] = strings.Repeat("G", 64)

	if _, err := buildGoBinaries(goPullConfig{version: goTestVersion, baseURL: goDistBaseURL, files: files}); err == nil {
		t.Fatal("expected error for 64-char non-hex hash even after lowercasing")
	}
}

// TestBuildGoBinaries_UppercaseHashNormalizedAndValid pins the same case-folding
// contract as the node path (review #1): UPPERCASE published hashes must be
// lowercased so the generated config passes config.ValidateRuntimes.
func TestBuildGoBinaries_UppercaseHashNormalizedAndValid(t *testing.T) {
	files := map[string]string{}
	const hexLetters = "ABCDEF"
	for i, spec := range goArchiveSpecs(goTestVersion) {
		files[spec.filename] = strings.Repeat(string(hexLetters[i%len(hexLetters)]), 64)
	}

	binaries, err := buildGoBinaries(goPullConfig{version: goTestVersion, baseURL: goDistBaseURL, files: files})
	if err != nil {
		t.Fatalf("buildGoBinaries: %v", err)
	}

	for _, spec := range goArchiveSpecs(goTestVersion) {
		info := binaries[spec.os][spec.arch][spec.libc]
		want := strings.ToLower(files[spec.filename])
		if info.Hash != want {
			t.Errorf("%s: Hash = %q, want lowercase %q", spec.filename, info.Hash, want)
		}
	}

	rt := config.RuntimeConfig{
		Kind:    config.RuntimeKindGo,
		Mode:    config.RuntimeModeManaged,
		Go:      &config.RuntimeConfigGo{GoVersion: goTestVersion},
		Managed: &config.RuntimeConfigManaged{Binaries: binaries},
	}
	if err := config.ValidateRuntimes(config.MapOfRuntimes{"go": rt}); err != nil {
		t.Fatalf("ValidateRuntimes rejected normalized go hashes: %v", err)
	}
}

func TestBuildGoRuntimeJSON(t *testing.T) {
	data := &GoRuntimeData{GoVersion: "1.26.3"}
	result := buildGoRuntimeJSON(data, make(binmanager.MapOfBinaries))

	if result.Kind != "go" {
		t.Errorf("Kind = %q, want %q", result.Kind, "go")
	}
	if result.Mode != "managed" {
		t.Errorf("Mode = %q, want %q", result.Mode, "managed")
	}
	if result.Go == nil {
		t.Fatal("Go config should not be nil")
	}
	if result.Go.GoVersion != "1.26.3" {
		t.Errorf("GoVersion = %q, want %q", result.Go.GoVersion, "1.26.3")
	}
	if result.UV != nil || result.JVM != nil || result.Node != nil {
		t.Error("only Go config should be set for a go runtime")
	}
}

func TestPullGoRuntime_Success(t *testing.T) {
	orig := getLatestGoRelease
	defer func() { getLatestGoRelease = orig }()

	getLatestGoRelease = func() (*registry.GoRelease, error) {
		return &registry.GoRelease{Version: goTestVersion, Files: buildGoTestFiles(goTestVersion)}, nil
	}

	data, binaries, err := pullGoRuntime()
	if err != nil {
		t.Fatalf("pullGoRuntime: %v", err)
	}
	if data.GoVersion != goTestVersion {
		t.Errorf("GoVersion = %q, want %q", data.GoVersion, goTestVersion)
	}
	for _, spec := range goArchiveSpecs(goTestVersion) {
		if _, ok := binaries[spec.os][spec.arch][spec.libc]; !ok {
			t.Errorf("%s/%s/%s: missing entry", spec.os, spec.arch, spec.libc)
		}
	}
}

// TestPullGoRuntime_LookupError verifies a release-lookup failure aborts the
// pull rather than emitting a config built on a stale fallback.
func TestPullGoRuntime_LookupError(t *testing.T) {
	orig := getLatestGoRelease
	defer func() { getLatestGoRelease = orig }()

	getLatestGoRelease = func() (*registry.GoRelease, error) {
		return nil, errors.New("simulated lookup failure")
	}

	data, binaries, err := pullGoRuntime()
	if err == nil {
		t.Fatal("expected pullGoRuntime to return an error on lookup failure")
	}
	if data != nil || binaries != nil {
		t.Error("expected nil data and binaries when the release lookup fails")
	}
}

// TestRunPullRuntimes_Go_GeneratesEntryWithGoVersion is the capstone for review
// #3 + the reported bug: `pull --runtime go` must generate a go entry with
// go.goVersion and verified hashes, and a SECOND pull (which carries over the
// just-written go entry through the read→merge→write round-trip) must NOT drop
// goVersion.
func TestRunPullRuntimes_Go_GeneratesEntryWithGoVersion(t *testing.T) {
	oldUpdate := pullRuntimesUpdateFlag
	oldRuntime := pullRuntimesRuntimeFlag
	oldDryRun := pullRuntimesDryRunFlag
	origGo := getLatestGoRelease
	defer func() {
		pullRuntimesUpdateFlag = oldUpdate
		pullRuntimesRuntimeFlag = oldRuntime
		pullRuntimesDryRunFlag = oldDryRun
		getLatestGoRelease = origGo
	}()

	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	pullRuntimesUpdateFlag = true
	pullRuntimesRuntimeFlag = "go"
	pullRuntimesDryRunFlag = false
	getLatestGoRelease = func() (*registry.GoRelease, error) {
		return &registry.GoRelease{Version: goTestVersion, Files: buildGoTestFiles(goTestVersion)}, nil
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")

	if err := runPullRuntimes(nil, []string{path}); err != nil {
		t.Fatalf("first pull: %v", err)
	}

	assertGoEntryValid := func(stage string) config.MapOfRuntimes {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: reading written file: %v", stage, err)
		}
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("%s: written file not valid JSON: %v", stage, err)
		}
		var goEntry struct {
			Go *GoConfigJSON `json:"go"`
		}
		if err := json.Unmarshal(parsed["go"], &goEntry); err != nil {
			t.Fatalf("%s: parsing written go entry: %v", stage, err)
		}
		if goEntry.Go == nil || goEntry.Go.GoVersion != goTestVersion {
			t.Fatalf("%s: go.goVersion missing/wrong: %+v", stage, goEntry.Go)
		}

		// The whole written config must load and validate (verified hashes).
		var runtimes config.MapOfRuntimes
		if err := json.Unmarshal(raw, &runtimes); err != nil {
			t.Fatalf("%s: unmarshaling runtimes config: %v", stage, err)
		}
		if err := config.ValidateRuntimes(runtimes); err != nil {
			t.Fatalf("%s: ValidateRuntimes: %v", stage, err)
		}
		return runtimes
	}

	assertGoEntryValid("first pull")

	// Second pull: go is regenerated again; either way the round-trip must keep
	// goVersion (regression guard for the reported data-loss bug).
	if err := runPullRuntimes(nil, []string{path}); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	assertGoEntryValid("second pull")
}
