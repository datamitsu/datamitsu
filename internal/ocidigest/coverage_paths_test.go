package ocidigest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSHA256Digest_Errors(t *testing.T) {
	good := "sha256:" + strings.Repeat("ab", 32)
	if _, err := parseSHA256Digest(good); err != nil {
		t.Fatalf("parseSHA256Digest(%q) = %v, want nil", good, err)
	}

	cases := map[string]string{
		"missing prefix": strings.Repeat("ab", 32),
		"wrong algo":     "sha512:" + strings.Repeat("ab", 32),
		"too short":      "sha256:abcd",
		"non-hex":        "sha256:" + strings.Repeat("zz", 32),
		"uppercase hex":  "sha256:" + strings.Repeat("AB", 32),
	}
	for name, dgst := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSHA256Digest(dgst); err == nil {
				t.Errorf("parseSHA256Digest(%q) = nil, want error", dgst)
			}
		})
	}
}

func TestNewResolver_DefaultsFromEnv(t *testing.T) {
	r := NewResolver()
	if r == nil {
		t.Fatal("NewResolver returned nil")
	}
	if r.Registry() == "" {
		t.Error("Registry() is empty; expected a default registry host")
	}
	if r.scheme != "https" {
		t.Errorf("scheme = %q, want https", r.scheme)
	}
	if r.bearerTokens == nil {
		t.Error("bearerTokens map not initialized")
	}
}

func TestResolver_Registry(t *testing.T) {
	r := NewResolverForHost("registry.example")
	if got := r.Registry(); got != "registry.example" {
		t.Errorf("Registry() = %q, want registry.example", got)
	}
}

func TestPullManifest_BadDigestInputRejected(t *testing.T) {
	r := newTestResolver("http://127.0.0.1:1", t.TempDir())
	if _, err := r.PullManifest(context.Background(), "owner/repo", "not-a-sha256-digest"); err == nil {
		t.Fatal("expected rejection of a malformed digest before any network call")
	}
}

func TestPullManifest_OversizeManifestRejected(t *testing.T) {
	body := make([]byte, manifestMaxBytes+1)
	for i := range body {
		body[i] = 'a'
	}
	digest := sha256Digest(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.PullManifest(context.Background(), "owner/repo", digest)
	if err == nil {
		t.Fatal("expected oversize rejection, got nil")
	}
	if !strings.Contains(err.Error(), "byte limit") {
		t.Errorf("error %q should mention the byte limit", err)
	}
}

func TestPullManifest_ServerErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.PullManifest(context.Background(), "owner/repo", sha256Digest([]byte("x")))
	if err == nil {
		t.Fatal("expected error on a 5xx manifest status")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error %q should carry the status code", err)
	}
}

// tokenChallenge builds a Bearer challenge whose realm targets the test server.
func tokenChallenge(serverURL string) string {
	return fmt.Sprintf(`Bearer realm="%s/token",service="test"`, serverURL)
}

func TestFetchToken_MalformedChallenge(t *testing.T) {
	r := newTestResolver("http://127.0.0.1:1", t.TempDir())
	if _, err := r.fetchToken(context.Background(), "Bearer service=svc"); err == nil {
		t.Fatal("expected malformed-challenge error (no realm)")
	}
}

func TestFetchToken_InvalidRealmURL(t *testing.T) {
	r := newTestResolver("http://127.0.0.1:1", t.TempDir())
	// A percent escape that does not parse forces url.Parse to fail.
	_, err := r.fetchToken(context.Background(), `Bearer realm="http://example.com/%zz"`)
	if err == nil {
		t.Fatal("expected invalid realm error")
	}
	if !strings.Contains(err.Error(), "realm") {
		t.Errorf("error %q should mention the realm", err)
	}
}

func TestFetchToken_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.fetchToken(context.Background(), tokenChallenge(srv.URL))
	if err == nil {
		t.Fatal("expected token endpoint status error")
	}
	if !strings.Contains(err.Error(), "status 403") {
		t.Errorf("error %q should carry the token status code", err)
	}
}

func TestFetchToken_UndecodableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.fetchToken(context.Background(), tokenChallenge(srv.URL))
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decode token response") {
		t.Errorf("error %q should describe the decode failure", err)
	}
}

func TestFetchToken_EmptyTokenRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.fetchToken(context.Background(), tokenChallenge(srv.URL))
	if err == nil {
		t.Fatal("expected no-token error")
	}
	if !strings.Contains(err.Error(), "no token") {
		t.Errorf("error %q should mention the missing token", err)
	}
}

func TestFetchToken_AccessTokenFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"fallback-token"}`))
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	got, err := r.fetchToken(context.Background(), tokenChallenge(srv.URL))
	if err != nil {
		t.Fatalf("fetchToken: %v", err)
	}
	if got != "fallback-token" {
		t.Errorf("token = %q, want the access_token fallback", got)
	}
}

func TestFetchToken_PassesServiceAndScope(t *testing.T) {
	var gotService, gotScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotService = r.URL.Query().Get("service")
		gotScope = r.URL.Query().Get("scope")
		_, _ = w.Write([]byte(`{"token":"scoped"}`))
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	challenge := fmt.Sprintf(`Bearer realm="%s/token",service="svc",scope="repository:owner/repo:pull"`, srv.URL)
	if _, err := r.fetchToken(context.Background(), challenge); err != nil {
		t.Fatalf("fetchToken: %v", err)
	}
	if gotService != "svc" {
		t.Errorf("service = %q, want svc", gotService)
	}
	if gotScope != "repository:owner/repo:pull" {
		t.Errorf("scope = %q, want the pull scope", gotScope)
	}
}

func TestSaveCachedDigest_MkdirFailure(t *testing.T) {
	// Place a regular file where the cache directory's parent must be, so
	// MkdirAll cannot create the digest cache directory underneath it.
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// path's directory is blocker/.oci-digests — MkdirAll fails since blocker is a file.
	path := filepath.Join(blocker, ".oci-digests", "key.json")
	err := saveCachedDigest(path, "ghcr.io", "owner/repo", "1.0.0", testDigest)
	if err == nil {
		t.Fatal("expected MkdirAll failure when the parent is a file")
	}
	if !strings.Contains(err.Error(), "cache directory") {
		t.Errorf("error %q should mention the cache directory", err)
	}
}
