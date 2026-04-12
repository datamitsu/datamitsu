package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type PyPIPackageInfo struct {
	Name        string
	Version     string
	Description string
}

type pypiResponse struct {
	Info pypiInfo `json:"info"`
}

type pypiInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Summary string `json:"summary"`
}

var pypiHTTPClient = &http.Client{Timeout: 15 * time.Second}

func GetPyPIPackageInfo(packageName string) (*PyPIPackageInfo, error) {
	url := fmt.Sprintf("https://pypi.org/pypi/%s/json", packageName)
	return getPyPIPackageInfoFromURL(url, packageName)
}

func getPyPIPackageInfoFromURL(url, packageName string) (*PyPIPackageInfo, error) {
	resp, err := pypiHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PyPI package %s: %w", packageName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("PyPI package %q not found", packageName)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("PyPI registry returned status %d for %s: %s", resp.StatusCode, packageName, string(body))
	}

	var result pypiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode PyPI response for %s: %w", packageName, err)
	}

	return &PyPIPackageInfo{
		Name:        result.Info.Name,
		Version:     result.Info.Version,
		Description: result.Info.Summary,
	}, nil
}
