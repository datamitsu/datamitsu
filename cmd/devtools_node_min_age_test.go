package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

func TestPullNodeCmd_HasMinAgeFlag(t *testing.T) {
	if pullNodeMinAge == nil {
		t.Fatal("pullNodeMinAge pointer was not wired up in init()")
	}
	if *pullNodeMinAge != minAgeFlagDefault {
		t.Errorf("default --min-age = %d, want sentinel %d", *pullNodeMinAge, minAgeFlagDefault)
	}
	if pullNodeCmd.Flags().Lookup("min-age") == nil {
		t.Fatal("--min-age flag was not registered on pull-node")
	}
}

// newNPMTestRegistry serves a /latest and full-metadata document for a single
// package, mirroring the two-step strategy GetNPMPackageInfoWithMinAge uses.
func newNPMTestRegistry(t *testing.T, pkg, latestVersion string, times map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if strings.HasSuffix(r.URL.Path, "/latest") {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"name":        pkg,
				"version":     latestVersion,
				"description": "test package",
			})
			return
		}
		versions := map[string]any{}
		for v := range times {
			if v == "created" || v == "modified" {
				continue
			}
			versions[v] = map[string]string{"description": "test package"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":        pkg,
			"description": "test package",
			"dist-tags":   map[string]string{"latest": latestVersion},
			"versions":    versions,
			"time":        times,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withNPMRegistry points the registry client at srv for the duration of fn.
func withNPMRegistry(t *testing.T, srv *httptest.Server, fn func()) {
	t.Helper()
	origBase := registry.SetNPMRegistryBaseURLForTesting(srv.URL)
	origClient := registry.SetNPMClientForTesting(srv.Client())
	defer func() {
		registry.SetNPMRegistryBaseURLForTesting(origBase)
		registry.SetNPMClientForTesting(origClient)
	}()
	fn()
}

func writeNodeApps(t *testing.T, dir string, apps nodeAppsJSON) string {
	t.Helper()
	path := filepath.Join(dir, "nodeApps.json")
	if err := writeNodeAppsJSON(path, apps); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPullNode_MinAgeSelectsOlderVersion(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	now := time.Now()
	// 2.0.0 is brand new (too fresh); 1.0.0 is two weeks old (eligible).
	times := map[string]string{
		"created": now.Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		"1.0.0":   now.Add(-14 * 24 * time.Hour).Format(time.RFC3339),
		"2.0.0":   now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	srv := newNPMTestRegistry(t, "demo", "2.0.0", times)

	dir := t.TempDir()
	path := writeNodeApps(t, dir, nodeAppsJSON{
		"demo": {PackageName: "demo", Version: "1.0.0"},
	})

	// minAge of one day filters out the one-hour-old 2.0.0, keeping 1.0.0.
	*pullNodeMinAge = 24 * 60
	defer func() { *pullNodeMinAge = minAgeFlagDefault }()
	nodeUpdateFlag = true
	defer func() { nodeUpdateFlag = false }()

	withNPMRegistry(t, srv, func() {
		if err := runPullNode(pullNodeCmd, []string{path}); err != nil {
			t.Fatalf("runPullNode: %v", err)
		}
	})

	updated, err := readNodeAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated["demo"].Version != "1.0.0" {
		t.Errorf("expected min-age to keep 1.0.0, got %q", updated["demo"].Version)
	}
}

func TestRunPullNode_MinAgeNoVersionOldEnoughSkips(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	now := time.Now()
	// Only a single, very fresh version exists — nothing is old enough.
	times := map[string]string{
		"created": now.Add(-1 * time.Hour).Format(time.RFC3339),
		"3.0.0":   now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	srv := newNPMTestRegistry(t, "fresh", "3.0.0", times)

	dir := t.TempDir()
	path := writeNodeApps(t, dir, nodeAppsJSON{
		"fresh": {PackageName: "fresh", Version: "2.0.0"},
	})

	*pullNodeMinAge = 24 * 60
	defer func() { *pullNodeMinAge = minAgeFlagDefault }()
	nodeUpdateFlag = true
	defer func() { nodeUpdateFlag = false }()

	withNPMRegistry(t, srv, func() {
		// No version old enough must be a skip-with-warning, not an error.
		if err := runPullNode(pullNodeCmd, []string{path}); err != nil {
			t.Fatalf("runPullNode should skip, not fail: %v", err)
		}
	})

	updated, err := readNodeAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated["fresh"].Version != "2.0.0" {
		t.Errorf("expected current version 2.0.0 to be kept, got %q", updated["fresh"].Version)
	}
}

func TestRunPullNode_MinAgeZeroTakesLatest(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	now := time.Now()
	times := map[string]string{
		"created": now.Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		"1.0.0":   now.Add(-14 * 24 * time.Hour).Format(time.RFC3339),
		"2.0.0":   now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	srv := newNPMTestRegistry(t, "demo", "2.0.0", times)

	dir := t.TempDir()
	path := writeNodeApps(t, dir, nodeAppsJSON{
		"demo": {PackageName: "demo", Version: "1.0.0"},
	})

	// --min-age 0 disables filtering: the brand-new 2.0.0 should win.
	*pullNodeMinAge = 0
	defer func() { *pullNodeMinAge = minAgeFlagDefault }()
	nodeUpdateFlag = true
	defer func() { nodeUpdateFlag = false }()

	withNPMRegistry(t, srv, func() {
		if err := runPullNode(pullNodeCmd, []string{path}); err != nil {
			t.Fatalf("runPullNode: %v", err)
		}
	})

	updated, err := readNodeAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated["demo"].Version != "2.0.0" {
		t.Errorf("expected --min-age 0 to take latest 2.0.0, got %q", updated["demo"].Version)
	}
}
