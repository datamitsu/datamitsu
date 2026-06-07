// Package ocidigest resolves an OCI image tag to its content digest (the
// SHA-256 manifest digest) via the registry HTTP API, with a local cache.
//
// It is used by `datamitsu devtools dockerfile` to pin the generated base image
// by digest. Resolution is best-effort: callers treat any returned error as
// "leave the FROM line unpinned and warn", never as a fatal failure. Only the
// transport is exercised here — no artifact is downloaded or executed, so the
// mandatory-hash policy (which governs runtime artifact downloads) does not
// apply; the resolved digest is itself an external SHA-256 value.
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
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpx"
)

const (
	defaultTimeout = 30 * time.Second
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

// Resolver resolves image tags to content digests against a single registry.
type Resolver struct {
	httpClient *http.Client
	registry   string // registry host, e.g. "ghcr.io"
	scheme     string // URL scheme, "https" in production (overridden in tests)
	token      string // GITHUB_TOKEN, used to mint scoped pull tokens
	cacheDir   string // local digest cache root
}

// NewResolver returns a Resolver configured from the environment: the registry
// host from DATAMITSU_OCI_REGISTRY (default ghcr.io) and GITHUB_TOKEN for auth.
func NewResolver() *Resolver {
	return &Resolver{
		httpClient: httpx.NewHardenedClient(defaultTimeout),
		registry:   env.GetOCIRegistry(),
		scheme:     "https",
		token:      os.Getenv("GITHUB_TOKEN"), //nolint:forbidigo // third-party token, not a datamitsu env var
		cacheDir:   env.GetCachePath(),
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
func (r *Resolver) Resolve(ctx context.Context, repo, tag string) (string, error) {
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", r.scheme, r.registry, repo, tag)

	digest, status, challenge, err := r.fetchManifestDigest(ctx, manifestURL, "")
	if err != nil {
		return "", err
	}

	if status == http.StatusUnauthorized && challenge != "" {
		token, tErr := r.fetchToken(ctx, challenge)
		if tErr != nil {
			return "", tErr
		}
		digest, status, _, err = r.fetchManifestDigest(ctx, manifestURL, token)
		if err != nil {
			return "", err
		}
	}

	if status != http.StatusOK {
		return "", fmt.Errorf("registry returned status %d for %s:%s", status, repo, tag)
	}
	if digest == "" {
		return "", fmt.Errorf("registry returned no digest for %s:%s", repo, tag)
	}
	return digest, nil
}

// fetchManifestDigest performs a GET for the manifest and returns the
// Docker-Content-Digest header, the HTTP status, and any Bearer challenge from a
// 401 response. The body is read and discarded (manifests are small) so the
// connection can be reused. GET (not HEAD) is used because the digest header is
// returned for both and GET is universally supported across registries.
func (r *Resolver) fetchManifestDigest(ctx context.Context, manifestURL, token string) (digest string, status int, challenge string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", 0, "", fmt.Errorf("create manifest request: %w", err)
	}
	req.Header.Set("Accept", acceptManifests)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", 0, "", fmt.Errorf("manifest request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))

	return resp.Header.Get("Docker-Content-Digest"), resp.StatusCode, resp.Header.Get("WWW-Authenticate"), nil
}

// fetchToken mints a bearer token from the realm advertised in a Bearer
// challenge, attaching GITHUB_TOKEN as Basic auth when available (GHCR accepts a
// PAT as the password to mint a scoped pull token, raising rate limits and
// granting access to private base images).
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
	if r.token != "" {
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
