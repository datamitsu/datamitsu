package remotecfg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const maxConfigSize = 10 * 1024 * 1024 // 10 MiB

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
			return fmt.Errorf("HTTPS to HTTP redirect rejected: %s", req.URL)
		}
		return nil
	},
}

// FetchRemoteConfig downloads a remote config file and verifies its SHA-256 hash.
// The expectedHash must be in the format "sha256:hexdigest" or plain hex digest.
func FetchRemoteConfig(url, expectedHash string) (string, error) {
	if expectedHash == "" {
		return "", fmt.Errorf("remote config %s: hash is required", url)
	}

	if err := validateHashFormat(expectedHash); err != nil {
		return "", fmt.Errorf("remote config %s: %w", url, err)
	}

	resp, err := httpClient.Get(url)
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

func validateHashFormat(expectedHash string) error {
	hexHash := expectedHash
	if len(expectedHash) > 7 && expectedHash[:7] == "sha256:" {
		hexHash = expectedHash[7:]
	}

	if len(hexHash) != 64 {
		return fmt.Errorf("invalid hash: expected 64 hex characters, got %d", len(hexHash))
	}

	for _, c := range hexHash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("invalid hash: contains non-lowercase-hex character %q", c)
		}
	}

	return nil
}

func verifyHash(data []byte, expectedHash, url string) error {
	hexHash := expectedHash
	if len(expectedHash) > 7 && expectedHash[:7] == "sha256:" {
		hexHash = expectedHash[7:]
	}

	h := sha256.Sum256(data)
	actualHash := hex.EncodeToString(h[:])

	if actualHash != hexHash {
		return fmt.Errorf("remote config %s: hash mismatch: expected %s, got %s", url, hexHash, actualHash)
	}
	return nil
}
