package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type NPMPackageInfo struct {
	Name        string
	Version     string
	Description string
}

type npmLatestResponse struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

var npmHTTPClient = &http.Client{Timeout: 15 * time.Second}

func GetNPMPackageInfo(packageName string) (*NPMPackageInfo, error) {
	url := fmt.Sprintf("https://registry.npmjs.org/%s/latest", packageName)
	return getNPMPackageInfoFromURL(url, packageName)
}

func getNPMPackageInfoFromURL(url, packageName string) (*NPMPackageInfo, error) {
	resp, err := npmHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch npm package %s: %w", packageName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("npm package %q not found", packageName)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("npm registry returned status %d for %s: %s", resp.StatusCode, packageName, string(body))
	}

	var result npmLatestResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode npm response for %s: %w", packageName, err)
	}

	return &NPMPackageInfo{
		Name:        result.Name,
		Version:     result.Version,
		Description: result.Description,
	}, nil
}
