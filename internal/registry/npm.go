package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// npmFullResponse is the full package metadata document returned by
// registry.npmjs.org/{package} — used to walk historical versions by upload time.
type npmFullResponse struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	DistTags    map[string]string         `json:"dist-tags"`
	Versions    map[string]npmVersionMeta `json:"versions"`
	Time        map[string]string         `json:"time"`
}

type npmVersionMeta struct {
	Description string `json:"description"`
}

var npmHTTPClient = &http.Client{Timeout: 15 * time.Second}

// npmRegistryBaseURL is the registry root; overridable in tests.
var npmRegistryBaseURL = "https://registry.npmjs.org"

func GetNPMPackageInfo(ctx context.Context, packageName string) (*NPMPackageInfo, error) {
	url := fmt.Sprintf("%s/%s/latest", npmRegistryBaseURL, npmPackagePath(packageName))
	return getNPMPackageInfoFromURL(ctx, url, packageName)
}

// npmPackagePath encodes a package name for use in a registry URL path.
// Scoped names (@scope/name) need the slash percent-encoded.
func npmPackagePath(packageName string) string {
	if strings.HasPrefix(packageName, "@") {
		return strings.Replace(packageName, "/", "%2f", 1)
	}
	return packageName
}

// isNPMPreRelease reports whether a semver string is a pre-release.
// Per SemVer, a pre-release is anything after a "-"; build metadata after "+"
// (e.g. 1.2.3+build.1) is NOT a pre-release. golang.org/x/mod/semver is not
// used here because npm versions lack the required "v" prefix.
func isNPMPreRelease(version string) bool {
	if i := strings.IndexByte(version, '+'); i >= 0 {
		version = version[:i]
	}
	return strings.Contains(version, "-")
}

// GetNPMPackageInfoWithMinAge returns the newest non-prerelease version that is
// at least minAgeMinutes old. When minAgeMinutes <= 0 it returns the latest
// version via the lightweight /latest endpoint (no filtering, no full fetch).
// Otherwise it fetches the full package metadata once — /latest carries no
// upload timestamp, so the full document is required to judge age — and either
// returns the "latest" dist-tag (when it is non-prerelease and old enough) or
// walks all versions for the newest non-prerelease old enough. Returns
// (nil, nil) when no version qualifies.
func GetNPMPackageInfoWithMinAge(ctx context.Context, packageName string, minAgeMinutes int) (*NPMPackageInfo, error) {
	if minAgeMinutes <= 0 {
		return GetNPMPackageInfo(ctx, packageName)
	}

	full, err := getNPMFullResponse(ctx, packageName)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-time.Duration(minAgeMinutes) * time.Minute)

	// Fast path: if the "latest" dist-tag is non-prerelease and old enough,
	// return it without walking every version.
	if latest := full.DistTags["latest"]; latest != "" && !isNPMPreRelease(latest) {
		if ts, ok := full.Time[latest]; ok {
			if t, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil && !t.After(cutoff) {
				return npmInfoFromFull(full, latest), nil
			}
		}
	}

	// Walk all versions, pick the newest non-prerelease old enough.
	var bestVersion string
	var bestTime time.Time
	for version := range full.Versions {
		if isNPMPreRelease(version) {
			continue
		}
		ts, ok := full.Time[version]
		if !ok {
			continue
		}
		t, parseErr := time.Parse(time.RFC3339, ts)
		if parseErr != nil {
			continue
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

	return npmInfoFromFull(full, bestVersion), nil
}

// npmInfoFromFull builds an NPMPackageInfo for version from a full metadata
// document, preferring the per-version description over the package-level one.
func npmInfoFromFull(full *npmFullResponse, version string) *NPMPackageInfo {
	desc := full.Description
	if vm, ok := full.Versions[version]; ok && vm.Description != "" {
		desc = vm.Description
	}
	return &NPMPackageInfo{
		Name:        full.Name,
		Version:     version,
		Description: desc,
	}
}

func getNPMFullResponse(ctx context.Context, packageName string) (*npmFullResponse, error) {
	url := fmt.Sprintf("%s/%s", npmRegistryBaseURL, npmPackagePath(packageName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := npmHTTPClient.Do(req)
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

	var result npmFullResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 100<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode npm response for %s: %w", packageName, err)
	}
	return &result, nil
}

func getNPMPackageInfoFromURL(ctx context.Context, url, packageName string) (*NPMPackageInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := npmHTTPClient.Do(req)
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
