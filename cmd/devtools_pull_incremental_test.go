package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

func TestApplyNodeResult(t *testing.T) {
	entry := nodeAppEntry{PackageName: "demo", Version: "1.0.0", Description: "old"}
	tests := []struct {
		name   string
		result npmVersionResult
		want   nodeAppEntry
	}{
		{"newer version and description", npmVersionResult{LatestVersion: "2.0.0", UpdateNeeded: true, Description: "new"}, nodeAppEntry{PackageName: "demo", Version: "2.0.0", Description: "new"}},
		{"up to date keeps the version", npmVersionResult{LatestVersion: "1.0.0", Description: "new"}, nodeAppEntry{PackageName: "demo", Version: "1.0.0", Description: "new"}},
		{"no description keeps the old one", npmVersionResult{LatestVersion: "2.0.0", UpdateNeeded: true}, nodeAppEntry{PackageName: "demo", Version: "2.0.0", Description: "old"}},
		{"nothing old enough changes nothing", npmVersionResult{}, entry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyNodeResult(entry, tt.result); got != tt.want {
				t.Errorf("applyNodeResult() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestApplyUVResult(t *testing.T) {
	entry := uvAppEntry{PackageName: "demo", Version: "1.0.0", Description: "old"}
	tests := []struct {
		name   string
		result pypiVersionResult
		want   uvAppEntry
	}{
		{"newer version and description", pypiVersionResult{LatestVersion: "2.0.0", UpdateNeeded: true, Description: "new"}, uvAppEntry{PackageName: "demo", Version: "2.0.0", Description: "new"}},
		{"up to date keeps the version", pypiVersionResult{LatestVersion: "1.0.0", Description: "new"}, uvAppEntry{PackageName: "demo", Version: "1.0.0", Description: "new"}},
		{"no description keeps the old one", pypiVersionResult{LatestVersion: "2.0.0", UpdateNeeded: true}, uvAppEntry{PackageName: "demo", Version: "2.0.0", Description: "old"}},
		{"nothing old enough changes nothing", pypiVersionResult{}, entry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyUVResult(entry, tt.result); got != tt.want {
				t.Errorf("applyUVResult() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// pull-node --update saves each package before it looks up the next, so a
// package that fails later, or a run cut short, loses nothing already pulled.
func TestRunPullNode_SavesEachPackageBeforeTheNext(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	dir := t.TempDir()
	path := writeNodeApps(t, dir, nodeAppsJSON{
		"alpha":  {PackageName: "alpha", Version: "0.9.0"},
		"beta":   {PackageName: "beta", Version: "0.9.0"},
		"broken": {PackageName: "broken", Version: "0.1.0"},
	})

	var mu sync.Mutex
	var alphaWhenBetaAsked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		switch name {
		case "broken":
			w.WriteHeader(http.StatusBadGateway)
			return
		case "beta":
			if apps, err := readNodeAppsJSON(path); err == nil {
				mu.Lock()
				alphaWhenBetaAsked = apps["alpha"].Version
				mu.Unlock()
			}
		}
		if strings.HasSuffix(r.URL.Path, "/latest") {
			_ = json.NewEncoder(w).Encode(map[string]string{"name": name, "version": "1.0.0", "description": name + " package"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": name, "description": name + " package",
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions":  map[string]any{"1.0.0": map[string]string{}},
			"time":      map[string]string{"1.0.0": "2020-01-01T00:00:00Z"},
		})
	}))
	t.Cleanup(srv.Close)

	*pullNodeMinAge = 0
	defer func() { *pullNodeMinAge = minAgeFlagDefault }()
	nodeUpdateFlag = true
	defer func() { nodeUpdateFlag = false }()

	var err error
	withNPMRegistry(t, srv, func() {
		_ = captureStderr(func() {
			_ = captureStdout(func() { err = runPullNode(pullNodeCmd, []string{path}) })
		})
	})
	if err == nil || err.Error() != "1 of 3 packages failed" {
		t.Fatalf("runPullNode() = %v, want 1 of 3 packages failed", err)
	}
	if alphaWhenBetaAsked != "1.0.0" {
		t.Errorf("when beta was looked up the file held alpha %q, want 1.0.0 saved already", alphaWhenBetaAsked)
	}
	apps, err := readNodeAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if apps["alpha"].Version != "1.0.0" || apps["beta"].Version != "1.0.0" || apps["broken"].Version != "0.1.0" {
		t.Errorf("file holds alpha=%s beta=%s broken=%s, want both updates and the failed package as it was",
			apps["alpha"].Version, apps["beta"].Version, apps["broken"].Version)
	}
	if apps["alpha"].Description != "alpha package" {
		t.Errorf("alpha description = %q, want the registry's", apps["alpha"].Description)
	}
}

// pull-uv --update saves each package before it looks up the next.
func TestRunPullUV_SavesEachPackageBeforeTheNext(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	dir := t.TempDir()
	path := writeUVApps(t, dir, uvAppsJSON{
		"alpha":  {PackageName: "alpha", Version: "0.9.0"},
		"beta":   {PackageName: "beta", Version: "0.9.0"},
		"broken": {PackageName: "broken", Version: "0.1.0"},
	})

	var mu sync.Mutex
	var alphaWhenBetaAsked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /pypi/{name}/json
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		name := parts[len(parts)-2]
		switch name {
		case "broken":
			w.WriteHeader(http.StatusBadGateway)
			return
		case "beta":
			if apps, err := readUVAppsJSON(path); err == nil {
				mu.Lock()
				alphaWhenBetaAsked = apps["alpha"].Version
				mu.Unlock()
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"info":     map[string]string{"name": name, "version": "1.0.0", "summary": name + " package"},
			"releases": map[string]any{"1.0.0": []map[string]any{{"upload_time_iso_8601": "2020-01-01T00:00:00Z"}}},
		})
	}))
	t.Cleanup(srv.Close)

	*pullUVMinAge = 0
	defer func() { *pullUVMinAge = minAgeFlagDefault }()
	uvUpdateFlag = true
	defer func() { uvUpdateFlag = false }()

	var err error
	withPyPIRegistry(t, srv, func() {
		_ = captureStderr(func() {
			_ = captureStdout(func() { err = runPullUV(pullUVCmd, []string{path}) })
		})
	})
	if err == nil || err.Error() != "1 of 3 packages failed" {
		t.Fatalf("runPullUV() = %v, want 1 of 3 packages failed", err)
	}
	if alphaWhenBetaAsked != "1.0.0" {
		t.Errorf("when beta was looked up the file held alpha %q, want 1.0.0 saved already", alphaWhenBetaAsked)
	}
	apps, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if apps["alpha"].Version != "1.0.0" || apps["beta"].Version != "1.0.0" || apps["broken"].Version != "0.1.0" {
		t.Errorf("file holds alpha=%s beta=%s broken=%s, want both updates and the failed package as it was",
			apps["alpha"].Version, apps["beta"].Version, apps["broken"].Version)
	}
}

// fakeRuntimePulls replaces pullOneRuntime for the test: each runtime answers
// with the entry or the error given, and before it answers the test can look
// at what the file holds.
func fakeRuntimePulls(t *testing.T, answers map[string]func() (*RuntimeJSON, error)) {
	t.Helper()
	orig := pullOneRuntime
	t.Cleanup(func() { pullOneRuntime = orig })
	pullOneRuntime = func(_ context.Context, name string, _ int) (*RuntimeJSON, error) {
		answer, ok := answers[name]
		if !ok {
			return nil, fmt.Errorf("no answer for %s", name)
		}
		return answer()
	}
}

func setPullRuntimesFlags(t *testing.T, runtime string, dryRun bool) {
	t.Helper()
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	oldUpdate, oldRuntime, oldDryRun, oldMinAge := pullRuntimesUpdateFlag, pullRuntimesRuntimeFlag, pullRuntimesDryRunFlag, *pullRuntimesMinAge
	t.Cleanup(func() {
		pullRuntimesUpdateFlag, pullRuntimesRuntimeFlag, pullRuntimesDryRunFlag, *pullRuntimesMinAge = oldUpdate, oldRuntime, oldDryRun, oldMinAge
	})
	pullRuntimesUpdateFlag, pullRuntimesRuntimeFlag, pullRuntimesDryRunFlag, *pullRuntimesMinAge = true, runtime, dryRun, 0
}

func entryOf(r *RuntimeJSON) func() (*RuntimeJSON, error) {
	return func() (*RuntimeJSON, error) { return r, nil }
}

// A runtime that fails keeps its previous entry; every runtime that succeeds
// is saved before the next one is pulled, not held back by the failure.
func TestRunPullRuntimes_SavesEachRuntimeBeforeTheNext(t *testing.T) {
	setPullRuntimesFlags(t, "", false)
	path := filepath.Join(t.TempDir(), "runtimes.json")
	oldGo := buildGoRuntimeJSON(&GoRuntimeData{GoVersion: "1.25.0"}, nil)
	if err := writeRuntimesJSON(path, RuntimesJSON{"go": oldGo}); err != nil {
		t.Fatal(err)
	}

	var pnpmWhenBunPulled string
	fakeRuntimePulls(t, map[string]func() (*RuntimeJSON, error){
		"pnpm": entryOf(buildPNPMRuntimeJSON(&PNPMRuntimeData{PNPMVersion: "12.3.4"}, testPNPMBinaries())),
		"bun": func() (*RuntimeJSON, error) {
			if saved, err := readRuntimesJSON(path); err == nil && saved["pnpm"] != nil && saved["pnpm"].PNPM != nil {
				pnpmWhenBunPulled = saved["pnpm"].PNPM.PNPMVersion
			}
			return buildBunRuntimeJSON(&BunRuntimeData{BunVersion: "1.4.1"}, nil), nil
		},
		"go":   func() (*RuntimeJSON, error) { return nil, errors.New("go.dev is down") },
		"jvm":  entryOf(buildJVMRuntimeJSON(&JVMRuntimeData{JavaVersion: "26"}, nil)),
		"node": entryOf(buildNodeRuntimeJSON(&NodeRuntimeData{NodeVersion: "26.8.0"}, nil)),
		"uv":   entryOf(buildUVRuntimeJSON(&UVRuntimeData{PythonVersion: "3.14.3"}, nil)),
	})

	var err error
	stderr := captureStderr(func() {
		_ = captureStdout(func() { err = runPullRuntimes(nil, []string{path}) })
	})
	if err == nil || err.Error() != "1 of 6 runtimes failed: go" {
		t.Fatalf("runPullRuntimes() = %v, want 1 of 6 runtimes failed: go\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"1 of 6 runtimes failed and are left as they were in " + path, "  go: go.dev is down"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if pnpmWhenBunPulled != "12.3.4" {
		t.Errorf("when bun was pulled the file held pnpm %q, want 12.3.4 saved already", pnpmWhenBunPulled)
	}

	saved, err := readRuntimesJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"pnpm": "pnpm=12.3.4,binaries=1",
		"bun":  "bun=1.4.1,binaries=0",
		"go":   "go=1.25.0,binaries=0",
		"jvm":  "java=26,binaries=0",
		"node": "node=26.8.0,binaries=0",
		"uv":   "python=3.14.3,binaries=0",
	} {
		if got := runtimeVersion(saved[name]); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// Without a pnpm entry in the file, a pnpm pull that fails takes Node and Bun
// with it — each would name a runtime the file lacks — and nothing else.
func TestRunPullRuntimes_MissingPNPMFailsOnlyItsDependents(t *testing.T) {
	setPullRuntimesFlags(t, "", false)
	path := filepath.Join(t.TempDir(), "runtimes.json")

	fakeRuntimePulls(t, map[string]func() (*RuntimeJSON, error){
		"pnpm": func() (*RuntimeJSON, error) { return nil, errors.New("npm is down") },
		"bun":  entryOf(buildBunRuntimeJSON(&BunRuntimeData{BunVersion: "1.4.1"}, nil)),
		"go":   entryOf(buildGoRuntimeJSON(&GoRuntimeData{GoVersion: "1.26.0"}, nil)),
		"jvm":  entryOf(buildJVMRuntimeJSON(&JVMRuntimeData{JavaVersion: "26"}, nil)),
		"node": entryOf(buildNodeRuntimeJSON(&NodeRuntimeData{NodeVersion: "26.8.0"}, nil)),
		"uv":   entryOf(buildUVRuntimeJSON(&UVRuntimeData{PythonVersion: "3.14.3"}, nil)),
	})

	var err error
	stderr := captureStderr(func() {
		_ = captureStdout(func() { err = runPullRuntimes(nil, []string{path}) })
	})
	if err == nil || err.Error() != "3 of 6 runtimes failed: pnpm, bun, node" {
		t.Fatalf("runPullRuntimes() = %v, want 3 of 6 runtimes failed: pnpm, bun, node\nstderr:\n%s", err, stderr)
	}
	saved, err := readRuntimesJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pnpm", "bun", "node"} {
		if saved[name] != nil {
			t.Errorf("%s was recorded although it failed", name)
		}
	}
	for _, name := range []string{"go", "jvm", "uv"} {
		if saved[name] == nil {
			t.Errorf("%s was not recorded", name)
		}
	}
}

// A dry run pulls everything and writes nothing, failure or not.
func TestRunPullRuntimes_DryRunWritesNothing(t *testing.T) {
	setPullRuntimesFlags(t, "", true)
	path := filepath.Join(t.TempDir(), "runtimes.json")

	fakeRuntimePulls(t, map[string]func() (*RuntimeJSON, error){
		"pnpm": entryOf(buildPNPMRuntimeJSON(&PNPMRuntimeData{PNPMVersion: "12.3.4"}, testPNPMBinaries())),
		"bun":  entryOf(buildBunRuntimeJSON(&BunRuntimeData{BunVersion: "1.4.1"}, nil)),
		"go":   func() (*RuntimeJSON, error) { return nil, errors.New("go.dev is down") },
		"jvm":  entryOf(buildJVMRuntimeJSON(&JVMRuntimeData{JavaVersion: "26"}, nil)),
		"node": entryOf(buildNodeRuntimeJSON(&NodeRuntimeData{NodeVersion: "26.8.0"}, nil)),
		"uv":   entryOf(buildUVRuntimeJSON(&UVRuntimeData{PythonVersion: "3.14.3"}, nil)),
	})

	var err error
	_ = captureStderr(func() {
		_ = captureStdout(func() { err = runPullRuntimes(nil, []string{path}) })
	})
	if err == nil || err.Error() != "1 of 6 runtimes failed: go" {
		t.Fatalf("runPullRuntimes() = %v, want 1 of 6 runtimes failed: go", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a dry run wrote %s", path)
	}
}

// A runtime whose pulled entry equals the one on file is not rewritten.
func TestRunPullRuntimes_UnchangedRuntimeIsNotRewritten(t *testing.T) {
	setPullRuntimesFlags(t, "go", false)
	path := filepath.Join(t.TempDir(), "runtimes.json")
	entry := buildGoRuntimeJSON(&GoRuntimeData{GoVersion: "1.26.0"}, nil)
	if err := os.WriteFile(path, []byte(`{"go":{"go":{"goVersion":"1.26.0"},"kind":"go","managed":{"binaries":null},"mode":"managed"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fakeRuntimePulls(t, map[string]func() (*RuntimeJSON, error){"go": entryOf(entry)})

	stdout := captureStdout(func() { err = runPullRuntimes(nil, []string{path}) })
	if err != nil {
		t.Fatalf("runPullRuntimes() = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("an unchanged runtime rewrote the file:\n%s", after)
	}
	if !strings.Contains(stdout, "Nothing changed in "+path) {
		t.Errorf("stdout lacks the nothing-changed line:\n%s", stdout)
	}
}

// temurinGitHub serves release listings for adoptium/temurin<major>-binaries:
// each major answers with one release of the given tag and age.
func temurinGitHub(t *testing.T, releases map[string]map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
		release, ok := releases[parts[1]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{release})
	}))
	t.Cleanup(srv.Close)
	githubBaseURL = srv.URL
	t.Cleanup(func() { githubBaseURL = "" })
}

func temurinRelease(tag, file string, published time.Time) map[string]any {
	return map[string]any{
		"tag_name":     tag,
		"published_at": published.UTC().Format(time.RFC3339),
		"assets": []any{map[string]any{
			"name":                 file,
			"browser_download_url": "https://example.test/" + file,
			"digest":               "sha256:" + testHash1,
		}},
	}
}

// In the days after a feature release ships, its only GA build is younger than
// the minimum release age; the JVM pull then takes the previous feature
// release instead of failing.
func TestPullJVMRuntime_FallsBackToPreviousFeatureRelease(t *testing.T) {
	orig := getTemurinMajorVersions
	t.Cleanup(func() { getTemurinMajorVersions = orig })
	getTemurinMajorVersions = func(context.Context) ([]string, error) { return []string{"27", "26", "25"}, nil }

	now := time.Now()
	temurinGitHub(t, map[string]map[string]any{
		"temurin27-binaries": temurinRelease("jdk-27+35", "OpenJDK27U-jdk_x64_linux_hotspot_27_35.tar.gz", now.Add(-6*24*time.Hour)),
		"temurin26-binaries": temurinRelease("jdk-26.0.2+10", "OpenJDK26U-jdk_x64_linux_hotspot_26.0.2_10.tar.gz", now.Add(-60*24*time.Hour)),
	})

	var data *JVMRuntimeData
	var err error
	stdout := captureStdout(func() { data, _, err = pullJVMRuntime(context.Background(), 7*24*60) })
	if err != nil {
		t.Fatalf("pullJVMRuntime() error = %v", err)
	}
	if data.JavaVersion != "26" {
		t.Errorf("JavaVersion = %q, want 26", data.JavaVersion)
	}
	if !strings.Contains(stdout, "No Java 27 release is at least 10080 minutes old; trying Java 26") {
		t.Errorf("stdout does not say why Java 27 was passed over:\n%s", stdout)
	}
}

func TestPullJVMRuntime_NoFeatureReleaseOldEnough(t *testing.T) {
	orig := getTemurinMajorVersions
	t.Cleanup(func() { getTemurinMajorVersions = orig })
	getTemurinMajorVersions = func(context.Context) ([]string, error) { return []string{"27", "26"}, nil }

	now := time.Now()
	temurinGitHub(t, map[string]map[string]any{
		"temurin27-binaries": temurinRelease("jdk-27+35", "OpenJDK27U-jdk_x64_linux_hotspot_27_35.tar.gz", now.Add(-time.Hour)),
		"temurin26-binaries": temurinRelease("jdk-26.0.3+9", "OpenJDK26U-jdk_x64_linux_hotspot_26.0.3_9.tar.gz", now.Add(-time.Hour)),
	})

	var err error
	_ = captureStdout(func() { _, _, err = pullJVMRuntime(context.Background(), 24*60) })
	if err == nil || !strings.Contains(err.Error(), "no release for Temurin (Java 27, 26) is at least 1440 minutes old") {
		t.Fatalf("pullJVMRuntime() error = %v, want one naming every feature release tried", err)
	}
}
