package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

// uvPythonMetadataBaseURL is the raw-content root of the uv repository. uv
// resolves `--python <version>` against a CPython download table compiled into
// the binary at build time, and that table lives at
// crates/uv-python/download-metadata.json in the tagged source tree — so the
// file at a given tag is exactly what that uv release can install.
var uvPythonMetadataBaseURL = "https://raw.githubusercontent.com/astral-sh/uv"

var uvPythonHTTPClient = httpx.NewHardenedClient(60 * time.Second)

// uvPythonEntry is the subset of a uv download-metadata.json record we consume.
// Entries cover CPython, PyPy and GraalPy across every platform, so a single
// interpreter version appears many times; only name/version identity matters
// here, not the per-platform URLs.
type uvPythonEntry struct {
	Name       string `json:"name"`
	Major      int    `json:"major"`
	Minor      int    `json:"minor"`
	Patch      int    `json:"patch"`
	Prerelease string `json:"prerelease"`
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
	tag := strings.TrimPrefix(uvVersion, "v")
	url := fmt.Sprintf("%s/%s/crates/uv-python/download-metadata.json", uvPythonMetadataBaseURL, tag)
	return getUVSupportedPythonVersionsFromURL(ctx, url)
}

func getUVSupportedPythonVersionsFromURL(ctx context.Context, url string) (map[string]bool, error) {
	if err := httpx.GuardOffline("uv Python metadata lookup"); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := uvPythonHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch uv Python metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("uv Python metadata request to %s returned status %d: %s", url, resp.StatusCode, string(body))
	}

	// The table is a few MiB and grows with every CPython release, so the cap is
	// well above the current size rather than the 10 MiB used for the small
	// endoflife.date payloads.
	var entries map[string]uvPythonEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode uv Python metadata: %w", err)
	}

	versions := filterStableCPythonVersions(entries)
	if len(versions) == 0 {
		return nil, fmt.Errorf("no stable CPython versions found in uv Python metadata at %s", url)
	}

	return versions, nil
}

// filterStableCPythonVersions reduces the per-platform metadata records to the
// set of stable CPython versions. PyPy/GraalPy entries and pre-releases (uv
// ships betas of the next line, which must never be pinned) are dropped.
func filterStableCPythonVersions(entries map[string]uvPythonEntry) map[string]bool {
	versions := make(map[string]bool)
	for _, e := range entries {
		if e.Name != "cpython" || e.Prerelease != "" {
			continue
		}
		versions[fmt.Sprintf("%d.%d.%d", e.Major, e.Minor, e.Patch)] = true
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
