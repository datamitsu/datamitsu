package remotecfg

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// --- CachedConfigPath tests ---

func TestCachedConfigPath(t *testing.T) {
	path := CachedConfigPath("/tmp/cache", "https://example.com/config.ts")

	if filepath.Dir(path) != filepath.Join("/tmp/cache", ".remote-configs") {
		t.Errorf("unexpected directory: %s", filepath.Dir(path))
	}

	if filepath.Ext(path) != ".ts" {
		t.Errorf("expected .ts extension, got %s", filepath.Ext(path))
	}

	expectedHash := hashutil.XXH3Hex([]byte("https://example.com/config.ts"))
	expectedName := expectedHash + ".ts"
	if filepath.Base(path) != expectedName {
		t.Errorf("expected filename %s, got %s", expectedName, filepath.Base(path))
	}
}

func TestCachedConfigPath_DifferentURLs(t *testing.T) {
	path1 := CachedConfigPath("/cache", "https://a.com/x.ts")
	path2 := CachedConfigPath("/cache", "https://b.com/y.ts")

	if path1 == path2 {
		t.Error("different URLs should produce different cache paths")
	}
}

func TestCachedConfigPath_SameURL(t *testing.T) {
	path1 := CachedConfigPath("/cache", "https://a.com/x.ts")
	path2 := CachedConfigPath("/cache", "https://a.com/x.ts")

	if path1 != path2 {
		t.Error("same URL should produce same cache path")
	}
}

// --- LoadCached tests ---

func TestLoadCached_Missing(t *testing.T) {
	_, err := LoadCached("/nonexistent/path")
	if !os.IsNotExist(err) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestLoadCached_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadCached(path)
	if err != nil {
		t.Fatal(err)
	}
	if content != "content" {
		t.Errorf("expected 'content', got %q", content)
	}
}

// --- SaveCached tests ---

func TestSaveCached_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.ts")

	if err := SaveCached(path, "hello world"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

// --- FetchRemoteConfig tests ---

func TestFetchRemoteConfig_Success(t *testing.T) {
	body := "export function getConfig(input) { return input; }"
	hash := sha256Hex(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	content, err := FetchRemoteConfig(srv.URL+"/config.ts", hash)
	if err != nil {
		t.Fatal(err)
	}
	if content != body {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestFetchRemoteConfig_Sha256Prefix(t *testing.T) {
	body := "config content"
	hash := "sha256:" + sha256Hex(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	content, err := FetchRemoteConfig(srv.URL, hash)
	if err != nil {
		t.Fatal(err)
	}
	if content != body {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestFetchRemoteConfig_HashMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("actual content"))
	}))
	defer srv.Close()

	_, err := FetchRemoteConfig(srv.URL, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

func TestFetchRemoteConfig_EmptyHash(t *testing.T) {
	_, err := FetchRemoteConfig("https://example.com/config.ts", "")
	if err == nil {
		t.Fatal("expected error for empty hash")
	}
}

func TestFetchRemoteConfig_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchRemoteConfig(srv.URL, "abc123")
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
}

// --- Resolve tests ---

func TestResolve_CacheHit(t *testing.T) {
	dir := t.TempDir()
	url := "https://example.com/cached-config.ts"
	body := "cached config"
	hash := sha256Hex(body)

	cachePath := CachedConfigPath(dir, url)
	if err := SaveCached(cachePath, body); err != nil {
		t.Fatal(err)
	}

	content, err := Resolve(url, hash, dir)
	if err != nil {
		t.Fatal(err)
	}
	if content != body {
		t.Errorf("expected cached content, got %q", content)
	}
}

func TestResolve_CacheMiss_Fetch(t *testing.T) {
	dir := t.TempDir()
	body := "fresh config from server"
	hash := sha256Hex(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	content, err := Resolve(srv.URL+"/config.ts", hash, dir)
	if err != nil {
		t.Fatal(err)
	}
	if content != body {
		t.Errorf("expected %q, got %q", body, content)
	}

	// Verify it was cached
	cachePath := CachedConfigPath(dir, srv.URL+"/config.ts")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal("expected config to be cached on disk")
	}
	if string(data) != body {
		t.Errorf("cached content mismatch: %q", string(data))
	}
}

func TestResolve_FetchError_NoCache(t *testing.T) {
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	closedURL := srv.URL + "/config.ts"
	srv.Close()

	_, err := Resolve(closedURL, "0000000000000000000000000000000000000000000000000000000000000000", dir)
	if err == nil {
		t.Fatal("expected error when fetch fails and no cache exists")
	}
}

func TestResolve_EmptyHash(t *testing.T) {
	_, err := Resolve("https://example.com/config.ts", "", "/tmp")
	if err == nil {
		t.Fatal("expected error for empty hash")
	}
}

func TestResolve_CacheHashMismatch_Refetches(t *testing.T) {
	dir := t.TempDir()
	oldBody := "old cached version"
	newBody := "new version from server"
	newHash := sha256Hex(newBody)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(newBody))
	}))
	defer srv.Close()

	// Write cache with old content
	cachePath := CachedConfigPath(dir, srv.URL+"/config.ts")
	if err := SaveCached(cachePath, oldBody); err != nil {
		t.Fatal(err)
	}

	// Resolve with new hash — should refetch
	content, err := Resolve(srv.URL+"/config.ts", newHash, dir)
	if err != nil {
		t.Fatal(err)
	}
	if content != newBody {
		t.Errorf("expected refetched content, got %q", content)
	}
}

func TestResolve_CacheHashMismatch_FetchFails(t *testing.T) {
	dir := t.TempDir()
	body := "cached content"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	srvURL := srv.URL + "/config.ts"
	cachePath := CachedConfigPath(dir, srvURL)
	if err := SaveCached(cachePath, body); err != nil {
		t.Fatal(err)
	}

	// Resolve with a hash that doesn't match — should fail (no stale fallback)
	_, err := Resolve(srvURL, "0000000000000000000000000000000000000000000000000000000000000000", dir)
	if err == nil {
		t.Fatal("expected error when cache hash doesn't match and fetch fails")
	}
}
