package registry

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

type temurinReleaseVersions struct {
	MostRecentFeatureRelease int   `json:"most_recent_feature_release"`
	AvailableReleases        []int `json:"available_releases"`
}

var temurinHTTPClient = &http.Client{Timeout: 15 * time.Second}

// GetTemurinMajorVersions returns the Temurin (Eclipse Adoptium) feature
// releases, newest first, starting at the most recent one. A caller walks down
// the list: the newest feature release can be too young for the minimum
// release age in the days after it ships.
func GetTemurinMajorVersions(ctx context.Context) ([]string, error) {
	return getTemurinMajorVersionsFromURL(ctx, "https://api.adoptium.net/v3/info/available_releases")
}

func getTemurinMajorVersionsFromURL(ctx context.Context, url string) ([]string, error) {
	if err := httpx.GuardOffline("Temurin release lookup"); err != nil {
		return nil, err
	}
	var releases temurinReleaseVersions
	if err := getJSON(ctx, temurinHTTPClient, "adoptium API", url, 10<<20, &releases); err != nil {
		return nil, fmt.Errorf("failed to fetch Temurin releases: %w", err)
	}

	versions := extractMajorVersions(releases)
	if len(versions) == 0 {
		return nil, errors.New("no major version found in Temurin releases")
	}
	return versions, nil
}

// extractMajorVersions orders the feature releases newest first, whatever
// order the API lists them in, and leaves out any above the most recent one.
func extractMajorVersions(releases temurinReleaseVersions) []string {
	majors := slices.Clone(releases.AvailableReleases)
	if releases.MostRecentFeatureRelease > 0 {
		majors = append(majors, releases.MostRecentFeatureRelease)
		majors = slices.DeleteFunc(majors, func(v int) bool { return v > releases.MostRecentFeatureRelease })
	}
	majors = slices.DeleteFunc(majors, func(v int) bool { return v <= 0 })
	slices.SortFunc(majors, func(a, b int) int { return cmp.Compare(b, a) })
	majors = slices.Compact(majors)

	versions := make([]string, len(majors))
	for i, v := range majors {
		versions[i] = strconv.Itoa(v)
	}
	return versions
}
