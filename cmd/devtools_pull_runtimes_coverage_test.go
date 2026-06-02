package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
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

	got, err := httpGetLimited(srv.Client(), srv.URL, 1024)
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

	_, err := httpGetLimited(srv.Client(), srv.URL, 1024)
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

	_, err := httpGetLimited(srv.Client(), srv.URL, maxSize)
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

	_, err := httpGetLimited(client, url, 1024)
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
	getLatestPythonStableVersion = func() (string, error) {
		return "3.14.3", errors.New("simulated lookup failure")
	}

	// minAge is irrelevant here: the Python lookup fails before any network call.
	data, binaries, err := pullUVRuntime(0)
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
	orig := getLatestTemurinMajorVersion
	defer func() { getLatestTemurinMajorVersion = orig }()

	getLatestTemurinMajorVersion = func() (string, error) {
		return "25", errors.New("simulated lookup failure")
	}

	// minAge is irrelevant here: the Temurin lookup fails before any network call.
	data, binaries, err := pullJVMRuntime(0)
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
