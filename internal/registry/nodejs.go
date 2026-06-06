package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const nodejsFallbackLTSVersion = "24.14.0"

type nodejsRelease struct {
	Cycle  string `json:"cycle"`
	Latest string `json:"latest"`
	LTS    any    `json:"lts"`
	EOL    any    `json:"eol"`
}

var nodejsHTTPClient = &http.Client{Timeout: 15 * time.Second}

// GetLatestNodeLTSVersion returns the latest non-EOL Node.js LTS version from
// endoflife.date, falling back to a pinned version on failure.
func GetLatestNodeLTSVersion(ctx context.Context) (string, error) {
	return getLatestNodeLTSVersionFromURL(ctx, "https://endoflife.date/api/nodejs.json")
}

func getLatestNodeLTSVersionFromURL(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nodejsFallbackLTSVersion, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := nodejsHTTPClient.Do(req)
	if err != nil {
		return nodejsFallbackLTSVersion, fmt.Errorf("failed to fetch Node.js releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nodejsFallbackLTSVersion, fmt.Errorf("endoflife.date returned status %d for nodejs: %s", resp.StatusCode, string(body))
	}

	var releases []nodejsRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&releases); err != nil {
		return nodejsFallbackLTSVersion, fmt.Errorf("failed to decode Node.js releases: %w", err)
	}

	version := filterLatestLTS(releases)
	if version == "" {
		return nodejsFallbackLTSVersion, errors.New("no LTS version found in Node.js releases")
	}

	return version, nil
}

func filterLatestLTS(releases []nodejsRelease) string {
	for _, r := range releases {
		if !isLTS(r) {
			continue
		}
		if parseEOLField(r.EOL) {
			continue
		}
		if r.Latest != "" {
			return r.Latest
		}
	}
	return ""
}

func isLTS(r nodejsRelease) bool {
	switch v := r.LTS.(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false"
	default:
		return false
	}
}
