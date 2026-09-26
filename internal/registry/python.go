package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

const pythonFallbackStableVersion = "3.14.3"

type pythonRelease struct {
	Cycle  string `json:"cycle"`
	Latest string `json:"latest"`
	EOL    any    `json:"eol"`
}

var pythonHTTPClient = &http.Client{Timeout: 15 * time.Second}

// GetLatestPythonStableVersion returns the latest non-EOL stable Python version
// from endoflife.date, falling back to a pinned version on failure.
func GetLatestPythonStableVersion(ctx context.Context) (string, error) {
	return getLatestPythonStableVersionFromURL(ctx, "https://endoflife.date/api/python.json")
}

func getLatestPythonStableVersionFromURL(ctx context.Context, url string) (string, error) {
	if err := httpx.GuardOffline("Python release lookup"); err != nil {
		return pythonFallbackStableVersion, err
	}
	var releases []pythonRelease
	if err := getJSON(ctx, pythonHTTPClient, "endoflife.date", url, 10<<20, &releases); err != nil {
		return pythonFallbackStableVersion, fmt.Errorf("failed to fetch Python releases: %w", err)
	}

	version := filterLatestStablePython(releases)
	if version == "" {
		return pythonFallbackStableVersion, errors.New("no stable version found in Python releases")
	}

	return version, nil
}

func filterLatestStablePython(releases []pythonRelease) string {
	for _, r := range releases {
		if parseEOLField(r.EOL) {
			continue
		}
		if r.Latest != "" {
			return r.Latest
		}
	}
	return ""
}
