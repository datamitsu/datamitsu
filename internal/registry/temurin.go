package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const temurinFallbackMajorVersion = "25"

type temurinReleaseVersions struct {
	MostRecentFeatureRelease int   `json:"most_recent_feature_release"`
	AvailableReleases        []int `json:"available_releases"`
}

var temurinHTTPClient = &http.Client{Timeout: 15 * time.Second}

// GetLatestTemurinMajorVersion returns the most recent Temurin (Eclipse Adoptium)
// feature-release major version, falling back to a pinned version on failure.
func GetLatestTemurinMajorVersion(ctx context.Context) (string, error) {
	return getLatestTemurinMajorVersionFromURL(ctx, "https://api.adoptium.net/v3/info/available_releases")
}

func getLatestTemurinMajorVersionFromURL(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return temurinFallbackMajorVersion, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := temurinHTTPClient.Do(req)
	if err != nil {
		return temurinFallbackMajorVersion, fmt.Errorf("failed to fetch Temurin releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return temurinFallbackMajorVersion, fmt.Errorf("adoptium API returned status %d: %s", resp.StatusCode, string(body))
	}

	var releases temurinReleaseVersions
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&releases); err != nil {
		return temurinFallbackMajorVersion, fmt.Errorf("failed to decode Temurin releases: %w", err)
	}

	version := extractMajorVersion(releases)
	if version == "" {
		return temurinFallbackMajorVersion, errors.New("no major version found in Temurin releases")
	}

	return version, nil
}

func extractMajorVersion(releases temurinReleaseVersions) string {
	if releases.MostRecentFeatureRelease > 0 {
		return strconv.Itoa(releases.MostRecentFeatureRelease)
	}
	if len(releases.AvailableReleases) > 0 {
		return strconv.Itoa(releases.AvailableReleases[0])
	}
	return ""
}
