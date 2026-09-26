package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
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
	if err := httpx.GuardOffline("Temurin release lookup"); err != nil {
		return temurinFallbackMajorVersion, err
	}
	var releases temurinReleaseVersions
	if err := getJSON(ctx, temurinHTTPClient, "adoptium API", url, 10<<20, &releases); err != nil {
		return temurinFallbackMajorVersion, fmt.Errorf("failed to fetch Temurin releases: %w", err)
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
