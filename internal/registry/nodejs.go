package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const nodejsFallbackLTSVersion = "24.14.0"

type nodejsRelease struct {
	Cycle  string      `json:"cycle"`
	Latest string      `json:"latest"`
	LTS    interface{} `json:"lts"`
	EOL    interface{} `json:"eol"`
}

var nodejsHTTPClient = &http.Client{Timeout: 15 * time.Second}

func GetLatestNodeLTSVersion() (string, error) {
	return getLatestNodeLTSVersionFromURL("https://endoflife.date/api/nodejs.json")
}

func getLatestNodeLTSVersionFromURL(url string) (string, error) {
	resp, err := nodejsHTTPClient.Get(url)
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
		return nodejsFallbackLTSVersion, fmt.Errorf("no LTS version found in Node.js releases")
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
