package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/spf13/cobra"
)

// === Part A.1: writeRuntimesJSON error paths ===

// TestWriteRuntimesJSON_CreateTempFails exercises the "creating temp file"
// branch: os.CreateTemp is given a directory that does not exist, so it fails
// before any data is written.
func TestWriteRuntimesJSON_CreateTempFails(t *testing.T) {
	dir := t.TempDir()
	// Parent directory "does-not-exist" is never created, so filepath.Dir(path)
	// points at a missing directory and os.CreateTemp must fail.
	path := filepath.Join(dir, "does-not-exist", "runtimes.json")

	err := writeRuntimesJSON(path, buildTestRuntimes())
	if err == nil {
		t.Fatal("expected error when the temp file's parent directory is missing")
	}
	if !strings.Contains(err.Error(), "creating temp file") {
		t.Errorf("error = %v, want it to mention \"creating temp file\"", err)
	}
}

// TestWriteRuntimesJSON_CreateTempFailsParentIsFile covers the same branch via a
// different failure mode: filepath.Dir(path) is a regular file, not a directory,
// so os.CreateTemp cannot create a sibling temp file inside it.
func TestWriteRuntimesJSON_CreateTempFailsParentIsFile(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "iam-a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seeding regular file: %v", err)
	}
	path := filepath.Join(notADir, "runtimes.json")

	err := writeRuntimesJSON(path, buildTestRuntimes())
	if err == nil {
		t.Fatal("expected error when the temp file's parent is a regular file")
	}
	if !strings.Contains(err.Error(), "creating temp file") {
		t.Errorf("error = %v, want it to mention \"creating temp file\"", err)
	}
}

// === Part A.2: httpGetLimited branches ===

func TestHTTPGetLimited_Success(t *testing.T) {
	const body = "hello world"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := httpGetLimited(context.Background(), srv.Client(), srv.URL, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", string(got), body)
	}
}

func TestHTTPGetLimited_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := httpGetLimited(context.Background(), srv.Client(), srv.URL, 1024)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("error = %v, want it to mention \"status\"", err)
	}
}

func TestHTTPGetLimited_ExceedsMaxSize(t *testing.T) {
	// Serve more bytes than maxSize so the LimitReader+guard trips.
	const maxSize = 8
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("A", maxSize+50)))
	}))
	defer srv.Close()

	_, err := httpGetLimited(context.Background(), srv.Client(), srv.URL, maxSize)
	if err == nil {
		t.Fatal("expected error when the body exceeds maxSize")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("error = %v, want it to mention \"exceeds maximum size\"", err)
	}
}

func TestHTTPGetLimited_TransportError(t *testing.T) {
	// Start a server, capture its URL, then close it so the GET fails at the
	// transport layer (connection refused) — exercising the GET error branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := srv.Client()
	url := srv.URL
	srv.Close()

	_, err := httpGetLimited(context.Background(), client, url, 1024)
	if err == nil {
		t.Fatal("expected transport error when the server is closed")
	}
	if !strings.Contains(err.Error(), "GET ") {
		t.Errorf("error = %v, want it to mention the wrapped GET failure", err)
	}
}

// === Part B: pull{UV,JVM}Runtime lookup-error paths via injectable seams ===

// TestPullUVRuntime_PythonLookupError verifies that a failed Python-version
// lookup aborts the UV pull with the wrapped error and returns nil data and
// binaries, rather than baking the registry's stale fallback into the config.
func TestPullUVRuntime_PythonLookupError(t *testing.T) {
	orig := getLatestPythonStableVersion
	defer func() { getLatestPythonStableVersion = orig }()

	// Mimic the real registry: it returns the fallback version AND an error.
	getLatestPythonStableVersion = func(_ context.Context) (string, error) {
		return "3.14.3", errors.New("simulated lookup failure")
	}

	// minAge is irrelevant here: the Python lookup fails before any network call.
	data, binaries, err := pullUVRuntime(context.Background(), 0)
	if err == nil {
		t.Fatal("expected pullUVRuntime to return an error on Python lookup failure")
	}
	if !strings.Contains(err.Error(), "failed to look up latest Python version") {
		t.Errorf("error = %v, want it to wrap \"failed to look up latest Python version\"", err)
	}
	if data != nil || binaries != nil {
		t.Error("expected nil data and binaries when the Python lookup fails")
	}
}

// TestPullJVMRuntime_TemurinLookupError verifies that a failed Temurin
// major-version lookup aborts the JVM pull with the wrapped error and returns
// nil data and binaries. The resolved major is interpolated into the upstream
// repo name, so a silent stale fallback would be especially dangerous here.
func TestPullJVMRuntime_TemurinLookupError(t *testing.T) {
	orig := getTemurinMajorVersions
	defer func() { getTemurinMajorVersions = orig }()

	getTemurinMajorVersions = func(_ context.Context) ([]string, error) {
		return nil, errors.New("simulated lookup failure")
	}

	// minAge is irrelevant here: the Temurin lookup fails before any network call.
	data, binaries, err := pullJVMRuntime(context.Background(), 0)
	if err == nil {
		t.Fatal("expected pullJVMRuntime to return an error on Temurin lookup failure")
	}
	if !strings.Contains(err.Error(), "failed to look up latest Temurin (Java) version") {
		t.Errorf("error = %v, want it to wrap \"failed to look up latest Temurin (Java) version\"", err)
	}
	if data != nil || binaries != nil {
		t.Error("expected nil data and binaries when the Temurin lookup fails")
	}
}

func TestSelectUVPythonDownloadsFiltersAndCanonicalizes(t *testing.T) {
	entries := map[string]registry.UVPythonDownload{
		"linux-x86_64": {
			Name: "cpython", Arch: registry.UVPythonArch{Family: "amd64"}, OS: "linux", Libc: "gnu",
			Major: 3, Minor: 14, Patch: 8, URL: "https://example.invalid/python.tar.gz", SHA256: strings.Repeat("A", 64), Build: "build-1",
		},
		"free-threaded": {
			Name: "cpython", Arch: registry.UVPythonArch{Family: "amd64", Variant: "freethreaded"}, OS: "linux", Libc: "gnu",
			Major: 3, Minor: 14, Patch: 8, URL: "https://example.invalid/python-ft.tar.gz", SHA256: strings.Repeat("b", 64),
		},
		"other-version": {Name: "cpython", Major: 3, Minor: 13, Patch: 9, URL: "https://example.invalid/old.tar.gz", SHA256: strings.Repeat("c", 64)},
	}
	selected, err := selectUVPythonDownloads(entries, "3.14.8")
	if err != nil {
		t.Fatalf("selectUVPythonDownloads() error = %v", err)
	}
	if len(selected) != 1 {
		t.Fatalf("selected entries = %#v, want only stable host-compatible build", selected)
	}
	for key, entry := range selected {
		if key != "linux-x86_64" || entry.Hash != "sha256:"+strings.Repeat("a", 64) || entry.Build != "build-1" {
			t.Fatalf("selected[%q] = %#v, hash must be canonical", key, entry)
		}
	}
}

func TestSelectUVPythonDownloadsRejectsMissingInvalidOrUnsupportedEntries(t *testing.T) {
	base := registry.UVPythonDownload{
		Name: "cpython", Arch: registry.UVPythonArch{Family: "definitely-unsupported"}, OS: "impossible", Libc: "none",
		Major: 3, Minor: 14, Patch: 8, URL: "https://example.invalid/python.tar.gz", SHA256: strings.Repeat("a", 64),
	}
	if got, err := selectUVPythonDownloads(map[string]registry.UVPythonDownload{"unsupported": base}, "3.14.8"); err == nil || got != nil {
		t.Fatalf("unsupported platform selection = %#v, %v; want an error and no entries", got, err)
	}
	base.Arch = registry.UVPythonArch{Family: "amd64"}
	base.OS, base.Libc = string(syslist.OsTypeLinux), "gnu"
	base.SHA256 = ""
	if got, err := selectUVPythonDownloads(map[string]registry.UVPythonDownload{"missing-pin": base}, "3.14.8"); err == nil || got != nil || !strings.Contains(err.Error(), "missing its URL or SHA-256") {
		t.Fatalf("missing hash selection = %#v, %v", got, err)
	}
	base.SHA256 = "not-a-hash"
	if got, err := selectUVPythonDownloads(map[string]registry.UVPythonDownload{"bad-pin": base}, "3.14.8"); err == nil || got != nil || !strings.Contains(err.Error(), "invalid SHA-256") {
		t.Fatalf("invalid hash selection = %#v, %v", got, err)
	}
}

func TestPullUVRuntimeRejectsMissingOrFailedPinnedCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/astral-sh/uv/releases" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"0.13.6","published_at":"2026-01-01T00:00:00Z","assets":[]}]`))
	}))
	defer server.Close()
	oldBase := githubBaseURL
	githubBaseURL = server.URL
	t.Cleanup(func() { githubBaseURL = oldBase })
	oldPython := getLatestPythonStableVersion
	getLatestPythonStableVersion = func(context.Context) (string, error) { return "3.14.8", nil }
	t.Cleanup(func() { getLatestPythonStableVersion = oldPython })
	stubUVSupportedPythonVersions(t, map[string]bool{"3.14.8": true}, nil)
	oldCatalog := getUVPythonDownloads
	t.Cleanup(func() { getUVPythonDownloads = oldCatalog })
	t.Run("empty catalog", func(t *testing.T) {
		getUVPythonDownloads = func(context.Context, string) (map[string]registry.UVPythonDownload, string, error) {
			return map[string]registry.UVPythonDownload{}, "0.13.6", nil
		}
		data, binaries, err := pullUVRuntime(context.Background(), 0)
		if err == nil || data != nil || binaries != nil || !strings.Contains(err.Error(), "no stable default-GIL entries") {
			t.Fatalf("pullUVRuntime() = %#v, %#v, %v", data, binaries, err)
		}
	})
	t.Run("catalog fetch failure", func(t *testing.T) {
		getUVPythonDownloads = func(context.Context, string) (map[string]registry.UVPythonDownload, string, error) {
			return nil, "", errors.New("catalog unavailable")
		}
		data, binaries, err := pullUVRuntime(context.Background(), 0)
		if err == nil || data != nil || binaries != nil || !strings.Contains(err.Error(), "failed to fetch Python downloads") {
			t.Fatalf("pullUVRuntime() = %#v, %#v, %v", data, binaries, err)
		}
	})
}

func TestRunPullRuntimesWritesOnlyChangedResults(t *testing.T) {
	oldUpdate, oldDryRun, oldFilter, oldMinAge, oldPull := pullRuntimesUpdateFlag, pullRuntimesDryRunFlag, pullRuntimesRuntimeFlag, pullRuntimesMinAge, pullOneRuntime
	t.Cleanup(func() {
		pullRuntimesUpdateFlag, pullRuntimesDryRunFlag, pullRuntimesRuntimeFlag, pullRuntimesMinAge, pullOneRuntime = oldUpdate, oldDryRun, oldFilter, oldMinAge, oldPull
	})
	pullRuntimesUpdateFlag, pullRuntimesDryRunFlag, pullRuntimesRuntimeFlag = true, false, "uv"
	minAge := 0
	pullRuntimesMinAge = &minAge
	current := &RuntimeJSON{Kind: "uv", Mode: "managed", UV: &UVConfigJSON{PythonVersion: "3.14.8"}}
	pullOneRuntime = func(context.Context, string, int) (*RuntimeJSON, error) { return current, nil }
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "runtimes.json")
	cmd := &cobra.Command{}
	if err := runPullRuntimes(cmd, []string{path}); err != nil {
		t.Fatalf("initial pull error = %v", err)
	}
	first, err := readRuntimesJSON(path)
	if err != nil || !reflect.DeepEqual(first["uv"], current) {
		t.Fatalf("saved runtime = %#v, err = %v", first["uv"], err)
	}
	if err := runPullRuntimes(cmd, []string{path}); err != nil {
		t.Fatalf("unchanged pull error = %v", err)
	}
	current = &RuntimeJSON{Kind: "uv", Mode: "managed", UV: &UVConfigJSON{PythonVersion: "3.14.9"}}
	pullRuntimesDryRunFlag = true
	if err := runPullRuntimes(cmd, []string{path}); err != nil {
		t.Fatalf("dry-run pull error = %v", err)
	}
	afterDryRun, err := readRuntimesJSON(path)
	if err != nil || !reflect.DeepEqual(afterDryRun["uv"], first["uv"]) {
		t.Fatalf("dry run changed runtime file: %#v, err = %v", afterDryRun["uv"], err)
	}
}

func TestRunPullRuntimesReportsPullFailures(t *testing.T) {
	oldUpdate, oldDryRun, oldFilter, oldMinAge, oldPull := pullRuntimesUpdateFlag, pullRuntimesDryRunFlag, pullRuntimesRuntimeFlag, pullRuntimesMinAge, pullOneRuntime
	t.Cleanup(func() {
		pullRuntimesUpdateFlag, pullRuntimesDryRunFlag, pullRuntimesRuntimeFlag, pullRuntimesMinAge, pullOneRuntime = oldUpdate, oldDryRun, oldFilter, oldMinAge, oldPull
	})
	pullRuntimesUpdateFlag, pullRuntimesDryRunFlag, pullRuntimesRuntimeFlag = true, false, "uv"
	minAge := 0
	pullRuntimesMinAge = &minAge
	pullOneRuntime = func(context.Context, string, int) (*RuntimeJSON, error) {
		return nil, errors.New("fixture pull failure")
	}
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	err := runPullRuntimes(&cobra.Command{}, []string{filepath.Join(t.TempDir(), "runtimes.json")})
	if err == nil || !strings.Contains(err.Error(), "uv") || !strings.Contains(err.Error(), "1 of 1") {
		t.Fatalf("runPullRuntimes() error = %v, want failed-runtime summary", err)
	}
}
