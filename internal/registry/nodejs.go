package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
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
	if err := httpx.GuardOffline("Node.js release lookup"); err != nil {
		return nodejsFallbackLTSVersion, err
	}
	var releases []nodejsRelease
	if err := getJSON(ctx, nodejsHTTPClient, "endoflife.date", url, 10<<20, &releases); err != nil {
		return nodejsFallbackLTSVersion, fmt.Errorf("failed to fetch Node.js releases: %w", err)
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
