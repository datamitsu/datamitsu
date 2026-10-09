package remotecfg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/httpx"
)

const maxConfigSize = 10 * 1024 * 1024 // 10 MiB

// httpClient fetches remote config over the shared hardened transport. The
// overall 30s budget stays tighter than the artifact-download clients because
// configs are small; the dialer/TLS/response-header sub-timeouts come from the
// shared defaults and are bounded by this overall budget anyway.
var httpClient = httpx.NewHardenedClient(30 * time.Second)

// FetchRemoteConfig downloads a remote config file and verifies its SHA-256 hash.
// The expectedHash must be in the format "sha256:hexdigest" or plain hex digest.
func FetchRemoteConfig(ctx context.Context, url, expectedHash string) (string, error) {
	if err := httpx.GuardOffline("remote config fetch of " + url); err != nil {
		return "", err
	}
	if expectedHash == "" {
		return "", fmt.Errorf("remote config %s: hash is required", url)
	}

	if _, err := parseExpectedHash(expectedHash); err != nil {
		return "", fmt.Errorf("remote config %s: %w", url, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch remote config %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("failed to fetch remote config %s: HTTP %d", url, resp.StatusCode)
	}

	if resp.ContentLength > maxConfigSize {
		return "", fmt.Errorf("remote config %s too large: %d bytes exceeds limit of %d", url, resp.ContentLength, maxConfigSize)
	}

	limitedReader := io.LimitReader(resp.Body, maxConfigSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read remote config %s: %w", url, err)
	}
	if int64(len(data)) > maxConfigSize {
		return "", fmt.Errorf("remote config %s too large: exceeds limit of %d bytes", url, maxConfigSize)
	}

	if err := verifyHash(data, expectedHash, url); err != nil {
		return "", err
	}

	return string(data), nil
}

// parseExpectedHash parses the declared pin once, leniently — a person copies
// this value from a registry page or a README, so the canonical prefix and the
// case are normalized rather than demanded.
func parseExpectedHash(expectedHash string) (digest.Digest, error) {
	return digest.ParseSHA256Loose(expectedHash)
}

func verifyHash(data []byte, expectedHash, url string) error {
	pin, err := parseExpectedHash(expectedHash)
	if err != nil {
		return fmt.Errorf("invalid hash: %w", err)
	}
	if actual := digest.SHA256Of(data); actual.Hex() != pin.Hex() {
		return fmt.Errorf("remote config %s: hash mismatch: expected %s, got %s", url, pin.String(), actual.String())
	}
	return nil
}
