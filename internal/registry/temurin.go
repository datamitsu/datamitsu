package registry

import (
	"encoding/json"
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

func GetLatestTemurinMajorVersion() (string, error) {
	return getLatestTemurinMajorVersionFromURL("https://api.adoptium.net/v3/info/available_releases")
}

func getLatestTemurinMajorVersionFromURL(url string) (string, error) {
	resp, err := temurinHTTPClient.Get(url)
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
		return temurinFallbackMajorVersion, fmt.Errorf("no major version found in Temurin releases")
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
