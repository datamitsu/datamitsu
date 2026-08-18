package ocidigest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newTestResolver builds a Resolver pointed at a test server URL.
func newTestResolver(serverURL, cacheDir string) *Resolver {
	u, _ := url.Parse(serverURL)
	return &Resolver{
		httpClient: httpx.NewHardenedClient(5 * time.Second),
		registry:   u.Host,
		scheme:     u.Scheme,
		cacheDir:   cacheDir,
	}
}

func TestResolve_BearerChallengeFlow(t *testing.T) {
	var tokenHits, manifestHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		tokenHits++
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		manifestHits++
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer realm="http://%s/token",service="test",scope="repository:datamitsu/datamitsu:pull"`, r.Host))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Docker-Content-Digest", testDigest)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	got, err := r.Resolve(context.Background(), "datamitsu/datamitsu", "1.2.3")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != testDigest {
		t.Errorf("digest = %q, want %q", got, testDigest)
	}
	if tokenHits != 1 {
		t.Errorf("tokenHits = %d, want 1", tokenHits)
	}
	if manifestHits != 2 {
		t.Errorf("manifestHits = %d, want 2 (unauth + retry)", manifestHits)
	}
}

func TestResolve_Anonymous(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Content-Digest", testDigest)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	got, err := r.Resolve(context.Background(), "datamitsu/datamitsu", "1.2.3")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != testDigest {
		t.Errorf("digest = %q, want %q", got, testDigest)
	}
}

func TestResolve_MissingDigestHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 but no Docker-Content-Digest
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.Resolve(context.Background(), "datamitsu/datamitsu", "1.2.3")
	if err == nil {
		t.Fatal("expected error when digest header is absent")
	}
	if !strings.Contains(err.Error(), "no digest") {
		t.Errorf("error = %v, want it to mention missing digest", err)
	}
}

func TestResolve_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	if _, err := r.Resolve(context.Background(), "datamitsu/datamitsu", "1.2.3"); err == nil {
		t.Fatal("expected error on 5xx")
	}
}

func TestResolveCached_SecondCallSkipsNetwork(t *testing.T) {
	var manifestHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		manifestHits++
		w.Header().Set("Docker-Content-Digest", testDigest)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())

	first, err := r.ResolveCached(context.Background(), "datamitsu/datamitsu", "1.2.3")
	if err != nil {
		t.Fatalf("first ResolveCached: %v", err)
	}
	second, err := r.ResolveCached(context.Background(), "datamitsu/datamitsu", "1.2.3")
	if err != nil {
		t.Fatalf("second ResolveCached: %v", err)
	}
	if first != testDigest || second != testDigest {
		t.Errorf("digests = %q, %q, want %q", first, second, testDigest)
	}
	if manifestHits != 1 {
		t.Errorf("manifestHits = %d, want 1 (second call served from cache)", manifestHits)
	}
}

func TestParseBearerChallenge(t *testing.T) {
	header := `Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:datamitsu/datamitsu:pull"`
	params := parseBearerChallenge(header)
	if params["realm"] != "https://ghcr.io/token" {
		t.Errorf("realm = %q", params["realm"])
	}
	if params["service"] != "ghcr.io" {
		t.Errorf("service = %q", params["service"])
	}
	if params["scope"] != "repository:datamitsu/datamitsu:pull" {
		t.Errorf("scope = %q", params["scope"])
	}
	if parseBearerChallenge("Basic realm=x") != nil {
		t.Error("non-Bearer challenge should return nil")
	}
}

// TestNewResolverForHostClientSplit pins the metadata/payload client split:
// manifest and token requests are deadline-bounded (small bodies), while blob
// streaming has no end-to-end deadline — large layers on slow links are
// bounded by the PullBlob progress watchdog instead.
func TestNewResolverForHostClientSplit(t *testing.T) {
	r := NewResolverForHost("registry.example")

	if r.registry != "registry.example" {
		t.Errorf("registry = %q, want %q", r.registry, "registry.example")
	}
	if r.scheme != "https" {
		t.Errorf("scheme = %q, want https", r.scheme)
	}
	if r.httpClient.Timeout != defaultTimeout {
		t.Errorf("metadata client Timeout = %v, want %v", r.httpClient.Timeout, defaultTimeout)
	}
	if r.blobClient.Timeout != 0 {
		t.Errorf("blob client Timeout = %v, want 0 (progress-guarded, not deadline-bounded)", r.blobClient.Timeout)
	}
	if r.bearerTokens == nil {
		t.Error("bearerTokens map not initialized")
	}
}

// TestFetchToken_ForeignRealmStaysAnonymous pins that GITHUB_TOKEN is never
// sent to a non-GHCR token realm: Docker Hub (and others) reject a foreign
// Basic credential with 401 instead of minting an anonymous token, which
// would break public pulls in any environment where GITHUB_TOKEN is set.
func TestFetchToken_ForeignRealmStaysAnonymous(t *testing.T) {
	var sawBasicAuth bool
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_, _, sawBasicAuth = r.BasicAuth()
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "anon-token"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer anon-token" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer realm="http://%s/token",service="test"`, r.Host))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Docker-Content-Digest", testDigest)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	r.token = "ghp_should-not-leak"
	got, err := r.Resolve(context.Background(), "owner/repo", "1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != testDigest {
		t.Errorf("digest = %q, want %q", got, testDigest)
	}
	if sawBasicAuth {
		t.Error("GITHUB_TOKEN was sent as Basic auth to a non-GHCR token realm")
	}
}

func TestMayAttachGitHubToken(t *testing.T) {
	tests := []struct {
		name      string
		registry  string
		token     string
		realmHost string
		want      bool
	}{
		{name: "ghcr to ghcr", registry: "ghcr.io", token: "t", realmHost: "ghcr.io", want: true},
		{name: "no token", registry: "ghcr.io", realmHost: "ghcr.io"},

		// The realm is whatever the registry's own WWW-Authenticate header
		// says, so a host of the attacker's choosing can claim GHCR's realm.
		// Minting a real GHCR token for it hands that token to the host that
		// asked, on the retried request.
		{name: "hostile registry claiming the ghcr realm", registry: "evil.example", token: "t", realmHost: "ghcr.io"},
		{name: "hostile lookalike realm", registry: "ghcr.io", token: "t", realmHost: "ghcr.io.evil.com"},

		// Not a leak, but a broken handshake: these registries reject a
		// GitHub token instead of falling back to an anonymous one.
		{name: "docker hub", registry: "index.docker.io", token: "t", realmHost: "auth.docker.io"},
		{name: "gitlab", registry: "gitlab.example.com", token: "t", realmHost: "gitlab.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Resolver{registry: tt.registry, token: tt.token}
			if got := r.mayAttachGitHubToken(tt.realmHost); got != tt.want {
				t.Errorf("mayAttachGitHubToken(registry=%q, realm=%q) = %v, want %v", tt.registry, tt.realmHost, got, tt.want)
			}
		})
	}
}

// End-to-end version of the relay case: a resolver configured for some other
// host must neither send Basic auth to the realm it was told about nor forward
// a bearer token to the registry that asked for it.
func TestFetchToken_NoGitHubTokenForForeignRegistry(t *testing.T) {
	var sawBasicAuth, sawBearer bool
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			sawBasicAuth = true
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "minted-token"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth == "" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer realm="http://%s/token",service="test",scope="repository:victim/private:pull"`, r.Host))
			w.WriteHeader(http.StatusUnauthorized)
			return
		} else if strings.HasPrefix(auth, "Bearer github-pat") {
			sawBearer = true
		}
		w.Header().Set("Docker-Content-Digest", testDigest)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	r.token = "github-pat"
	if _, err := r.Resolve(context.Background(), "victim/private", "1.0.0"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sawBasicAuth {
		t.Error("GITHUB_TOKEN was sent as Basic auth to a registry that is not GHCR")
	}
	if sawBearer {
		t.Error("GITHUB_TOKEN was forwarded verbatim as a bearer credential")
	}
}
