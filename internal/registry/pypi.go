package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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

// pypiFullResponse is the full package metadata returned by
// pypi.org/pypi/{package}/json — its releases map holds every version's
// distribution files, used to walk historical versions by upload time.
type pypiFullResponse struct {
	Info     pypiInfo                     `json:"info"`
	Releases map[string][]pypiReleaseFile `json:"releases"`
}

type pypiReleaseFile struct {
	UploadTime        string `json:"upload_time"`
	UploadTimeISO8601 string `json:"upload_time_iso_8601"`
	Yanked            bool   `json:"yanked"`
}

var pypiHTTPClient = &http.Client{Timeout: 15 * time.Second}

// pypiBaseURL is the registry root; overridable in tests.
var pypiBaseURL = "https://pypi.org"

func GetPyPIPackageInfo(ctx context.Context, packageName string) (*PyPIPackageInfo, error) {
	url := fmt.Sprintf("%s/pypi/%s/json", pypiBaseURL, packageName)
	return getPyPIPackageInfoFromURL(ctx, url, packageName)
}

// pypiPreReleaseRe conservatively matches PEP 440 pre-release / dev markers:
// alpha (a1), beta (b2), release candidate (rc1) and dev releases (.dev3).
// Post-releases (.post1) are intentionally NOT matched — they are stable.
var pypiPreReleaseRe = regexp.MustCompile(`(?i)(a|b|rc)\d+|\.dev\d+`)

func isPyPIPreRelease(version string) bool {
	return pypiPreReleaseRe.MatchString(version)
}

// GetPyPIPackageInfoWithMinAge returns the newest version that is at least
// minAgeMinutes old, skipping PEP 440 pre-release and yanked versions. A
// version's effective release time is the earliest upload_time among its
// non-yanked files; a version is only treated as yanked when ALL its files are
// yanked. When minAgeMinutes <= 0 it returns the latest version (no filtering).
// Returns (nil, nil) when no version qualifies.
func GetPyPIPackageInfoWithMinAge(ctx context.Context, packageName string, minAgeMinutes int) (*PyPIPackageInfo, error) {
	full, err := getPyPIFullResponse(ctx, packageName)
	if err != nil {
		return nil, err
	}

	if minAgeMinutes <= 0 {
		return &PyPIPackageInfo{
			Name:        full.Info.Name,
			Version:     full.Info.Version,
			Description: full.Info.Summary,
		}, nil
	}

	cutoff := time.Now().Add(-time.Duration(minAgeMinutes) * time.Minute)

	var bestVersion string
	var bestTime time.Time
	for version, files := range full.Releases {
		if isPyPIPreRelease(version) {
			continue
		}
		t, ok := pypiVersionReleaseTime(files)
		if !ok {
			continue // fully yanked or no parsable upload time
		}
		if t.After(cutoff) {
			continue
		}
		if bestVersion == "" || t.After(bestTime) {
			bestVersion = version
			bestTime = t
		}
	}

	if bestVersion == "" {
		// No version qualifies under the min-age cutoff; callers branch on a nil
		// result as a normal, non-error outcome (documented contract).
		return nil, nil //nolint:nilnil
	}

	return &PyPIPackageInfo{
		Name:        full.Info.Name,
		Version:     bestVersion,
		Description: full.Info.Summary,
	}, nil
}

// pypiVersionReleaseTime returns the earliest upload time among a version's
// non-yanked files. ok is false when every file is yanked (or none has a
// parsable timestamp), which callers treat as "skip this version".
func pypiVersionReleaseTime(files []pypiReleaseFile) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, f := range files {
		if f.Yanked {
			continue
		}
		t, ok := parsePyPIUploadTime(f)
		if !ok {
			continue
		}
		if !found || t.Before(earliest) {
			earliest = t
			found = true
		}
	}
	return earliest, found
}

// parsePyPIUploadTime parses a release file's timestamp, preferring the
// ISO 8601 field (RFC3339) and falling back to the legacy upload_time layout
// ("2006-01-02T15:04:05", UTC implied).
func parsePyPIUploadTime(f pypiReleaseFile) (time.Time, bool) {
	if f.UploadTimeISO8601 != "" {
		if t, err := time.Parse(time.RFC3339, f.UploadTimeISO8601); err == nil {
			return t, true
		}
	}
	if f.UploadTime != "" {
		if t, err := time.Parse("2006-01-02T15:04:05", f.UploadTime); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func getPyPIFullResponse(ctx context.Context, packageName string) (*pypiFullResponse, error) {
	url := fmt.Sprintf("%s/pypi/%s/json", pypiBaseURL, packageName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := pypiHTTPClient.Do(req)
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

	var result pypiFullResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 100<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode PyPI response for %s: %w", packageName, err)
	}
	return &result, nil
}

func getPyPIPackageInfoFromURL(ctx context.Context, url, packageName string) (*PyPIPackageInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := pypiHTTPClient.Do(req)
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
