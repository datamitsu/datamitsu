package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

// uvPythonMetadataBaseURL is the raw-content root of the uv repository. uv
// resolves `--python <version>` against a CPython download table compiled into
// the binary at build time, and that table lives in the tagged source tree —
// so the file at a given tag is exactly what that uv release can install.
//
// uv 0.12.24 split the uv-python crate and the table moved from
// crates/uv-python/ to crates/uv-python-managed/; older tags keep only the old
// path, so both are tried, newest layout first.
var uvPythonMetadataBaseURL = "https://raw.githubusercontent.com/astral-sh/uv"

var uvPythonMetadataPaths = []string{
	"crates/uv-python-managed/download-metadata.json",
	"crates/uv-python/download-metadata.json",
}

var uvPythonHTTPClient = httpx.NewHardenedClient(60 * time.Second)

// uvPythonEntry keeps existing registry tests expressed in the catalog DTO.
type uvPythonEntry = UVPythonDownload

// UVPythonDownload preserves one CPython record from uv's download catalog.
type UVPythonDownload struct {
	Name       string       `json:"name"`
	Arch       UVPythonArch `json:"arch"`
	OS         string       `json:"os"`
	Libc       string       `json:"libc"`
	Major      int          `json:"major"`
	Minor      int          `json:"minor"`
	Patch      int          `json:"patch"`
	Prerelease string       `json:"prerelease"`
	URL        string       `json:"url"`
	SHA256     string       `json:"sha256"`
	Variant    string       `json:"variant"`
	Build      string       `json:"build"`
}

// UVPythonArch preserves the catalog's CPU family and optional build variant.
type UVPythonArch struct {
	Family  string `json:"family"`
	Variant string `json:"variant"`
}

// Version identifies the exact stable CPython patch represented by this entry.
func (e UVPythonDownload) Version() string {
	return fmt.Sprintf("%d.%d.%d", e.Major, e.Minor, e.Patch)
}

// GetUVSupportedPythonVersions returns the set of stable CPython versions (e.g.
// "3.14.6") that the given uv release is able to install, keyed by version
// string.
//
// This is the guard against pinning a Python that the pinned uv cannot fetch:
// python.org (via endoflife.date) publishes a patch release immediately, while
// uv only learns about it in the next uv release, which the minimum-release-age
// gate then holds back further. Installing under such a pin fails with
// "No interpreter found for Python <version>".
func GetUVSupportedPythonVersions(ctx context.Context, uvVersion string) (map[string]bool, error) {
	versions, _, err := GetUVSupportedPythonMetadata(ctx, uvVersion)
	return versions, err
}

// GetUVSupportedPythonMetadata returns stable CPython versions supported by
// uv and the exact release tag used to read its bundled metadata.
func GetUVSupportedPythonMetadata(ctx context.Context, uvVersion string) (map[string]bool, string, error) {
	tag := strings.TrimPrefix(uvVersion, "v")
	var lastErr error
	for _, path := range uvPythonMetadataPaths {
		url := fmt.Sprintf("%s/%s/%s", uvPythonMetadataBaseURL, tag, path)
		versions, err := getUVSupportedPythonVersionsFromURL(ctx, url)
		if err == nil {
			return versions, tag, nil
		}
		if !errors.Is(err, errNotFound) {
			return nil, "", err
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("uv %s publishes no Python download metadata: %w", tag, lastErr)
}

// GetUVPythonDownloads reads artifact metadata from one uv release, preserving upstream catalog keys.
func GetUVPythonDownloads(ctx context.Context, uvVersion string) (map[string]UVPythonDownload, string, error) {
	tag := strings.TrimPrefix(uvVersion, "v")
	var lastErr error
	for _, path := range uvPythonMetadataPaths {
		url := fmt.Sprintf("%s/%s/%s", uvPythonMetadataBaseURL, tag, path)
		entries, err := getUVPythonDownloadsFromURL(ctx, url)
		if err == nil {
			return entries, tag, nil
		}
		if !errors.Is(err, errNotFound) {
			return nil, "", err
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("uv %s publishes no Python download metadata: %w", tag, lastErr)
}

func getUVPythonDownloadsFromURL(ctx context.Context, url string) (map[string]UVPythonDownload, error) {
	if err := httpx.GuardOffline("uv Python metadata lookup"); err != nil {
		return nil, err
	}
	var entries map[string]UVPythonDownload
	if err := getJSON(ctx, uvPythonHTTPClient, "uv Python metadata", url, 64<<20, &entries); err != nil {
		return nil, fmt.Errorf("failed to fetch uv Python metadata from %s: %w", url, err)
	}
	if len(filterStableCPythonVersions(entries)) == 0 {
		return nil, fmt.Errorf("no stable CPython versions found in uv Python metadata at %s", url)
	}
	return entries, nil
}

func getUVSupportedPythonVersionsFromURL(ctx context.Context, url string) (map[string]bool, error) {
	entries, err := getUVPythonDownloadsFromURL(ctx, url)
	if err != nil {
		return nil, err
	}
	return filterStableCPythonVersions(entries), nil
}

func filterStableCPythonVersions(entries map[string]UVPythonDownload) map[string]bool {
	versions := make(map[string]bool)
	for _, e := range entries {
		if e.Name == "cpython" && e.Prerelease == "" {
			versions[e.Version()] = true
		}
	}
	return versions
}

// LatestSupportedPythonPatch returns the newest version in supported that is on
// want's major.minor line and no newer than want, or "" when the line is absent
// from supported entirely.
//
// The pin only ever moves backwards along the same line: dropping to an older
// minor (3.14.x -> 3.13.x) would silently change the interpreter users get, so
// that case is left for the caller to reject.
func LatestSupportedPythonPatch(supported map[string]bool, want string) string {
	prefix, ok := pythonMinorLinePrefix(want)
	if !ok {
		return ""
	}

	best := ""
	for version := range supported {
		if !strings.HasPrefix(version, prefix) {
			continue
		}
		if semver.Compare("v"+version, "v"+want) > 0 {
			continue
		}
		if best == "" || semver.Compare("v"+version, "v"+best) > 0 {
			best = version
		}
	}

	return best
}

// pythonMinorLinePrefix turns "3.14.7" into the "3.14." prefix shared by every
// patch release on that line.
func pythonMinorLinePrefix(version string) (string, bool) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "." + parts[1] + ".", true
}
