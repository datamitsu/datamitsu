package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// goDevBody renders a go.dev/dl?mode=json payload from the given releases so the
// fetcher's JSON parsing and version/hash extraction can be exercised offline.
func goDevBody(releases []goDevRelease) string {
	b, _ := json.Marshal(releases)
	return string(b)
}

func TestGetLatestGoRelease(t *testing.T) {
	t.Run("returns highest stable version and per-file hashes", func(t *testing.T) {
		releases := []goDevRelease{
			{
				Version: "go1.26.3",
				Stable:  true,
				Files: []goDevFile{
					{Filename: "go1.26.3.src.tar.gz", Kind: "source", SHA256: "1111111111111111111111111111111111111111111111111111111111111111"},
					{Filename: "go1.26.3.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", Kind: "archive", SHA256: "2222222222222222222222222222222222222222222222222222222222222222"},
					{Filename: "go1.26.3.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64", Kind: "archive", SHA256: "3333333333333333333333333333333333333333333333333333333333333333"},
				},
			},
			{
				Version: "go1.25.7",
				Stable:  true,
				Files: []goDevFile{
					{Filename: "go1.25.7.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", Kind: "archive", SHA256: "4444444444444444444444444444444444444444444444444444444444444444"},
				},
			},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(goDevBody(releases)))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		rel, err := getLatestGoReleaseFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rel.Version != "1.26.3" {
			t.Errorf("Version = %q, want 1.26.3 (go prefix stripped)", rel.Version)
		}
		if got := rel.Files["go1.26.3.linux-amd64.tar.gz"]; got != "2222222222222222222222222222222222222222222222222222222222222222" {
			t.Errorf("linux-amd64 hash = %q", got)
		}
		if got := rel.Files["go1.26.3.darwin-arm64.tar.gz"]; got != "3333333333333333333333333333333333333333333333333333333333333333" {
			t.Errorf("darwin-arm64 hash = %q", got)
		}
		// Files from the older release must not leak in.
		if _, ok := rel.Files["go1.25.7.linux-amd64.tar.gz"]; ok {
			t.Error("files from the non-selected release should not be present")
		}
	})

	t.Run("highest version wins regardless of order", func(t *testing.T) {
		releases := []goDevRelease{
			{Version: "go1.25.7", Stable: true, Files: []goDevFile{{Filename: "go1.25.7.linux-amd64.tar.gz", SHA256: "aaaa"}}},
			{Version: "go1.26.3", Stable: true, Files: []goDevFile{{Filename: "go1.26.3.linux-amd64.tar.gz", SHA256: "bbbb"}}},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(goDevBody(releases)))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		rel, err := getLatestGoReleaseFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rel.Version != "1.26.3" {
			t.Errorf("Version = %q, want 1.26.3", rel.Version)
		}
	})

	t.Run("skips unstable releases", func(t *testing.T) {
		releases := []goDevRelease{
			{Version: "go1.27rc1", Stable: false, Files: []goDevFile{{Filename: "go1.27rc1.linux-amd64.tar.gz", SHA256: "cccc"}}},
			{Version: "go1.26.3", Stable: true, Files: []goDevFile{{Filename: "go1.26.3.linux-amd64.tar.gz", SHA256: "dddd"}}},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(goDevBody(releases)))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		rel, err := getLatestGoReleaseFromURL(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rel.Version != "1.26.3" {
			t.Errorf("Version = %q, want 1.26.3 (unstable rc skipped)", rel.Version)
		}
	})

	t.Run("no stable releases errors", func(t *testing.T) {
		releases := []goDevRelease{
			{Version: "go1.27rc1", Stable: false, Files: []goDevFile{{Filename: "x", SHA256: "y"}}},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(goDevBody(releases)))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		if _, err := getLatestGoReleaseFromURL(server.URL); err == nil {
			t.Fatal("expected error when no stable release is present")
		}
	})

	t.Run("stable release with no hashed files errors", func(t *testing.T) {
		releases := []goDevRelease{
			{Version: "go1.26.3", Stable: true, Files: []goDevFile{{Filename: "go1.26.3.src.tar.gz", SHA256: ""}}},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(goDevBody(releases)))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		if _, err := getLatestGoReleaseFromURL(server.URL); err == nil {
			t.Fatal("expected error when the stable release has no files with hashes")
		}
	})

	t.Run("stable release with empty version errors", func(t *testing.T) {
		// Version is literally "go": after TrimPrefix("go", "go") the version
		// is the empty string, which must be rejected rather than yielding a
		// release with a blank Version field.
		releases := []goDevRelease{
			{Version: "go", Stable: true, Files: []goDevFile{{Filename: "go.src.tar.gz", SHA256: "1111111111111111111111111111111111111111111111111111111111111111"}}},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(goDevBody(releases)))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		if _, err := getLatestGoReleaseFromURL(server.URL); err == nil {
			t.Fatal("expected error when the stable release version is empty after stripping the go prefix")
		}
	})

	t.Run("empty body errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("[]"))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		if _, err := getLatestGoReleaseFromURL(server.URL); err == nil {
			t.Fatal("expected error for empty release list")
		}
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		if _, err := getLatestGoReleaseFromURL(server.URL); err == nil {
			t.Fatal("expected error for non-200 status")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		orig := goDevHTTPClient
		goDevHTTPClient = server.Client()
		defer func() { goDevHTTPClient = orig }()

		if _, err := getLatestGoReleaseFromURL(server.URL); err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})

	t.Run("connection error", func(t *testing.T) {
		if _, err := getLatestGoReleaseFromURL("http://127.0.0.1:1"); err == nil {
			t.Fatal("expected error for unreachable host")
		}
	})
}

func TestGoVersionLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"go1.26.2", "go1.26.3", true},
		{"go1.26.3", "go1.26.2", false},
		{"go1.25.0", "go1.26.0", true},
		{"go1.26.3", "go1.26.3", false},
		{"go1.26", "go1.26.1", true},
		// Non-semver inputs fall through to a plain string comparison
		// (semver.IsValid is false for these), exercising the `return a < b`
		// fallback rather than semver.Compare.
		{"garbage", "go-weird", "garbage" < "go-weird"},
		{"go-weird", "garbage", "go-weird" < "garbage"},
		{"go1.26.3", "garbage", "go1.26.3" < "garbage"},
		{"garbage", "go1.26.3", "garbage" < "go1.26.3"},
		{"garbage", "garbage", false},
	}
	for _, tt := range tests {
		if got := goVersionLess(tt.a, tt.b); got != tt.want {
			t.Errorf("goVersionLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
