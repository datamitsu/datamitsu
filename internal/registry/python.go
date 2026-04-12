package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const pythonFallbackStableVersion = "3.14.3"

type pythonRelease struct {
	Cycle  string      `json:"cycle"`
	Latest string      `json:"latest"`
	EOL    interface{} `json:"eol"`
}

var pythonHTTPClient = &http.Client{Timeout: 15 * time.Second}

func GetLatestPythonStableVersion() (string, error) {
	return getLatestPythonStableVersionFromURL("https://endoflife.date/api/python.json")
}

func getLatestPythonStableVersionFromURL(url string) (string, error) {
	resp, err := pythonHTTPClient.Get(url)
	if err != nil {
		return pythonFallbackStableVersion, fmt.Errorf("failed to fetch Python releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return pythonFallbackStableVersion, fmt.Errorf("endoflife.date returned status %d for python: %s", resp.StatusCode, string(body))
	}

	var releases []pythonRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&releases); err != nil {
		return pythonFallbackStableVersion, fmt.Errorf("failed to decode Python releases: %w", err)
	}

	version := filterLatestStablePython(releases)
	if version == "" {
		return pythonFallbackStableVersion, fmt.Errorf("no stable version found in Python releases")
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

