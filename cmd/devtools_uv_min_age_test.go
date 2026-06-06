package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

func TestPullUVCmd_HasMinAgeFlag(t *testing.T) {
	if pullUVMinAge == nil {
		t.Fatal("pullUVMinAge pointer was not wired up in init()")
	}
	if *pullUVMinAge != minAgeFlagDefault {
		t.Errorf("default --min-age = %d, want sentinel %d", *pullUVMinAge, minAgeFlagDefault)
	}
	if pullUVCmd.Flags().Lookup("min-age") == nil {
		t.Fatal("--min-age flag was not registered on pull-uv")
	}
}

// newPyPITestRegistry serves the full /pypi/{pkg}/json document for a single
// package. releases maps each version to one upload timestamp (RFC3339); a
// version is marked yanked when its name is present in yanked.
func newPyPITestRegistry(t *testing.T, pkg, latestVersion string, releases map[string]string, yanked map[string]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		rel := map[string]any{}
		for v, ts := range releases {
			rel[v] = []map[string]any{
				{
					"upload_time_iso_8601": ts,
					"yanked":               yanked[v],
				},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"info": map[string]string{
				"name":    pkg,
				"version": latestVersion,
				"summary": "test package",
			},
			"releases": rel,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withPyPIRegistry points the registry client at srv for the duration of fn.
func withPyPIRegistry(t *testing.T, srv *httptest.Server, fn func()) {
	t.Helper()
	origBase := registry.SetPyPIBaseURLForTesting(srv.URL)
	origClient := registry.SetPyPIClientForTesting(srv.Client())
	defer func() {
		registry.SetPyPIBaseURLForTesting(origBase)
		registry.SetPyPIClientForTesting(origClient)
	}()
	fn()
}

func writeUVApps(t *testing.T, dir string, apps uvAppsJSON) string {
	t.Helper()
	path := filepath.Join(dir, "uvApps.json")
	if err := writeUVAppsJSON(path, apps); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPullUV_MinAgeSelectsOlderVersion(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	now := time.Now()
	// 2.0.0 is brand new (too fresh); 1.0.0 is two weeks old (eligible).
	releases := map[string]string{
		"1.0.0": now.Add(-14 * 24 * time.Hour).Format(time.RFC3339),
		"2.0.0": now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	srv := newPyPITestRegistry(t, "demo", "2.0.0", releases, nil)

	dir := t.TempDir()
	path := writeUVApps(t, dir, uvAppsJSON{
		"demo": {PackageName: "demo", Version: "1.0.0"},
	})

	// minAge of one day filters out the one-hour-old 2.0.0, keeping 1.0.0.
	*pullUVMinAge = 24 * 60
	defer func() { *pullUVMinAge = minAgeFlagDefault }()
	uvUpdateFlag = true
	defer func() { uvUpdateFlag = false }()

	withPyPIRegistry(t, srv, func() {
		if err := runPullUV(pullUVCmd, []string{path}); err != nil {
			t.Fatalf("runPullUV: %v", err)
		}
	})

	updated, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated["demo"].Version != "1.0.0" {
		t.Errorf("expected min-age to keep 1.0.0, got %q", updated["demo"].Version)
	}
}

func TestRunPullUV_MinAgeNoVersionOldEnoughSkips(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	now := time.Now()
	// Only a single, very fresh version exists — nothing is old enough.
	releases := map[string]string{
		"3.0.0": now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	srv := newPyPITestRegistry(t, "fresh", "3.0.0", releases, nil)

	dir := t.TempDir()
	path := writeUVApps(t, dir, uvAppsJSON{
		"fresh": {PackageName: "fresh", Version: "2.0.0"},
	})

	*pullUVMinAge = 24 * 60
	defer func() { *pullUVMinAge = minAgeFlagDefault }()
	uvUpdateFlag = true
	defer func() { uvUpdateFlag = false }()

	withPyPIRegistry(t, srv, func() {
		// No version old enough must be a skip-with-warning, not an error.
		if err := runPullUV(pullUVCmd, []string{path}); err != nil {
			t.Fatalf("runPullUV should skip, not fail: %v", err)
		}
	})

	updated, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated["fresh"].Version != "2.0.0" {
		t.Errorf("expected current version 2.0.0 to be kept, got %q", updated["fresh"].Version)
	}
}

func TestRunPullUV_MinAgeZeroTakesLatest(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	now := time.Now()
	releases := map[string]string{
		"1.0.0": now.Add(-14 * 24 * time.Hour).Format(time.RFC3339),
		"2.0.0": now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	srv := newPyPITestRegistry(t, "demo", "2.0.0", releases, nil)

	dir := t.TempDir()
	path := writeUVApps(t, dir, uvAppsJSON{
		"demo": {PackageName: "demo", Version: "1.0.0"},
	})

	// --min-age 0 disables filtering: the brand-new 2.0.0 should win.
	*pullUVMinAge = 0
	defer func() { *pullUVMinAge = minAgeFlagDefault }()
	uvUpdateFlag = true
	defer func() { uvUpdateFlag = false }()

	withPyPIRegistry(t, srv, func() {
		if err := runPullUV(pullUVCmd, []string{path}); err != nil {
			t.Fatalf("runPullUV: %v", err)
		}
	})

	updated, err := readUVAppsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated["demo"].Version != "2.0.0" {
		t.Errorf("expected --min-age 0 to take latest 2.0.0, got %q", updated["demo"].Version)
	}
}
