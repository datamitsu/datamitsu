// Package ocidigest is datamitsu's thin OCI registry v2 client. It resolves
// image tags to content digests (with a local cache) and pulls manifest and
// blob content with digest verification.
//
// Tag resolution is used by `datamitsu devtools dockerfile` to pin the
// generated base image by digest; it is best-effort and only the transport is
// exercised — no artifact is downloaded or executed. The pull path
// (PullManifest/PullBlob) is used by internal/ocibundle to seed the store from
// an OCI bundle; there every byte is verified against the sha256 digest chain
// before use.
package ocidigest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/httpx"
)

const (
	// defaultTimeout bounds manifest/token requests end-to-end. These carry
	// at most a few MiB, so even a noisy 1 Mbps VPN fits comfortably; the
	// old 30s left no headroom for that. Blob streaming deliberately does
	// NOT use this client — see blobClient.
	defaultTimeout = 120 * time.Second
	maxBodyBytes   = 1 << 20
)

// acceptManifests lists the manifest and index media types we accept, so the
// registry returns (and computes Docker-Content-Digest over) the multi-arch
// index when one exists — that index digest is what `FROM image@sha256:...`
// should pin so a single FROM works across platforms.
var acceptManifests = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Resolver resolves image tags to content digests and pulls manifest/blob
// content against a single registry.
type Resolver struct {
	httpClient *http.Client
	registry   string // registry host, e.g. "ghcr.io"
	scheme     string // URL scheme, "https" in production (overridden in tests)
	token      string // GITHUB_TOKEN, used to mint scoped pull tokens

	cacheDir string // local digest cache root

	// blobClient streams layer blobs. It has NO overall timeout: bundle
	// layers run to hundreds of MiB and an end-to-end deadline would abort
	// any download slower than size/timeout (a 120 MiB layer at 3 MiB/s
	// needs ~40s — the old shared 30s client cut it off mid-stream every
	// time). Stalls are caught instead by the per-attempt progress watchdog
	// in PullBlob.
	blobClient *http.Client

	// bearerMu guards bearerTokens, the in-process cache of minted bearer
	// tokens keyed by repository (the scope is repository:<repo>:pull, so the
	// repo key is scope-equivalent). Without it every request would repeat the
	// GET→401→token→GET handshake.
	bearerMu     sync.Mutex
	bearerTokens map[string]string
}

// NewResolver returns a Resolver configured from the environment: the registry
// host from DATAMITSU_OCI_REGISTRY (default ghcr.io) and GITHUB_TOKEN for auth.
func NewResolver() *Resolver {
	return NewResolverForHost(env.GetOCIRegistry())
}

// NewResolverForHost returns a Resolver against an explicit registry host —
// for bundle pulls, where the host comes from the config's oci.ref rather
// than DATAMITSU_OCI_REGISTRY.
func NewResolverForHost(host string) *Resolver {
	return &Resolver{
		httpClient:   httpx.NewHardenedClient(defaultTimeout),
		blobClient:   httpx.NewHardenedClient(0), // no overall deadline; see field doc
		registry:     host,
		scheme:       "https",
		token:        os.Getenv("GITHUB_TOKEN"), //nolint:forbidigo // third-party token, not a datamitsu env var
		cacheDir:     env.GetCachePath(),
		bearerTokens: make(map[string]string),
	}
}

// Registry returns the registry host the resolver targets.
func (r *Resolver) Registry() string { return r.registry }

// ResolveCached returns the content digest for repo:tag, consulting the local
// cache first. On a cache miss it resolves over the network and records the
// result. A failure to write the cache is non-fatal — the digest is returned.
func (r *Resolver) ResolveCached(ctx context.Context, repo, tag string) (string, error) {
	path := digestCachePath(r.cacheDir, r.registry, repo, tag)
	if digest, ok := loadCachedDigest(path); ok {
		return digest, nil
	}
	digest, err := r.Resolve(ctx, repo, tag)
	if err != nil {
		return "", err
	}
	_ = saveCachedDigest(path, r.registry, repo, tag, digest)
	return digest, nil
}

// Resolve returns the content digest (sha256:...) for repo:tag over the network,
// performing the registry bearer-token handshake when challenged with a 401.
// GET (not HEAD) is used because the Docker-Content-Digest header is returned
// for both and GET is universally supported across registries; the body is
// discarded (manifests are small) so the connection can be reused.
func (r *Resolver) Resolve(ctx context.Context, repo, tag string) (string, error) {
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", r.scheme, r.registry, repo, tag)

	resp, err := r.authedGet(ctx, nil, repo, manifestURL, acceptManifests)
	if err != nil {
		return "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned status %d for %s:%s", resp.StatusCode, repo, tag)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("registry returned no digest for %s:%s", repo, tag)
	}
	return digest, nil
}

// authedGet performs a GET against a registry endpoint, transparently handling
// the bearer-token handshake: a cached token for the repository is attached
// when present; on a 401 with a Bearer challenge a token is minted (and
// cached), and the request is retried once. The caller owns the response body.
// A nil client uses the default (deadline-bounded) manifest client; blob
// streaming passes the deadline-free blobClient.
func (r *Resolver) authedGet(ctx context.Context, client *http.Client, repo, rawURL, accept string) (*http.Response, error) {
	// Permanent: offline mode will not lift between attempts, so retrying only
	// spends the backoff schedule before reporting the same refusal. Mirrors
	// the offline guard on the binmanager download path.
	if err := httpx.GuardOffline("oci registry request to " + r.registry); err != nil {
		return nil, httpretry.Permanent(err)
	}
	if client == nil {
		client = r.httpClient
	}
	do := func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create registry request: %w", err)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("registry request failed: %w", err)
		}
		return resp, nil
	}

	resp, err := do(r.cachedBearerToken(repo))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	_ = resp.Body.Close()
	if challenge == "" {
		return nil, fmt.Errorf("registry returned 401 without a bearer challenge for %s", repo)
	}

	token, err := r.fetchToken(ctx, challenge)
	if err != nil {
		return nil, err
	}
	r.storeBearerToken(repo, token)

	return do(token)
}

func (r *Resolver) cachedBearerToken(repo string) string {
	r.bearerMu.Lock()
	defer r.bearerMu.Unlock()
	return r.bearerTokens[repo]
}

func (r *Resolver) storeBearerToken(repo, token string) {
	r.bearerMu.Lock()
	defer r.bearerMu.Unlock()
	if r.bearerTokens == nil {
		r.bearerTokens = make(map[string]string)
	}
	r.bearerTokens[repo] = token
}

// ghcrHost is the one registry whose token endpoint accepts a GitHub token.
const ghcrHost = "ghcr.io"

// mayAttachGitHubToken reports whether GITHUB_TOKEN may be sent as Basic auth
// to a token realm. Both the registry being pulled from and the realm the
// challenge advertises must be GHCR.
//
// The realm check alone is necessary but NOT sufficient, and reading it as a
// host check is how the credential leaks: the realm arrives in the registry's
// own WWW-Authenticate header, so a reference pointed at any host can name
// ghcr.io as its realm, have a real GHCR token minted at a scope of its
// choosing, and then receive that token as the Bearer credential on the retried
// request. Requiring the configured registry to be GHCR too keeps the token
// with the host it belongs to.
//
// The realm check remains because only GHCR accepts a GitHub token as the Basic
// password; any other registry (Docker Hub, GitLab, ...) treats the credential
// as real and fails the handshake with 401 instead of falling back to an
// anonymous token — so sending it would break pulls of public images whenever
// GITHUB_TOKEN is set (i.e. in any CI).
func (r *Resolver) mayAttachGitHubToken(realmHost string) bool {
	return r.token != "" && r.registry == ghcrHost && realmHost == ghcrHost
}

// fetchToken mints a bearer token from the realm advertised in a Bearer
// challenge, attaching GITHUB_TOKEN as Basic auth when the realm is GHCR's
// (GHCR accepts a PAT as the password to mint a scoped pull token, raising
// rate limits and granting access to private base images).
func (r *Resolver) fetchToken(ctx context.Context, challenge string) (string, error) {
	params := parseBearerChallenge(challenge)
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("malformed bearer challenge: %q", challenge)
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("invalid token realm %q: %w", realm, err)
	}
	q := u.Query()
	if svc := params["service"]; svc != "" {
		q.Set("service", svc)
	}
	if scope := params["scope"]; scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	if r.mayAttachGitHubToken(u.Host) {
		req.SetBasicAuth("x-access-token", r.token)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", errors.New("token endpoint returned no token")
}

// parseBearerChallenge extracts the comma-separated key="value" parameters from
// a `Bearer ...` WWW-Authenticate header.
func parseBearerChallenge(header string) map[string]string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return nil
	}
	params := map[string]string{}
	for part := range strings.SplitSeq(strings.TrimPrefix(header, prefix), ",") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		params[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"`)
	}
	return params
}
