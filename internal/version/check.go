// Package version compares the running datamitsu version against the minimum
// version a config requires, treating unstable builds as an advisory skip.
package version

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// IsUnstable reports whether v matches the release pipeline's unstable build
// format: "0.0.0-unstable.<date>.<sha>" (optionally with a "v" prefix).
// Other prerelease shapes (e.g. "-rc.1", "-beta.1", or "-unstable" appearing
// elsewhere in the string) are NOT unstable builds and remain subject to the
// version check.
func IsUnstable(v string) bool {
	return strings.HasPrefix(strings.TrimPrefix(v, "v"), "0.0.0-unstable.")
}

// CompareVersions returns skipped=true (with nil err) when current is an
// unstable build, signalling the caller to log an advisory warning instead
// of enforcing required. Unstable builds sort below v0.0.0 under semver
// prerelease rules, so enforcing the check would block every config.
//
// The local-build default "dev" (a plain `go build` with no -ldflags) is an
// unversioned source build: it satisfies any requirement silently (skipped=false,
// nil err), so developers can run a `go build` binary against real configs
// without the v99.99.99 ldflags dance used by `task build`.
//
// required is validated as semver even on the unstable/dev paths so config
// authors see a typo immediately rather than only when stable users run their
// config.
func CompareVersions(current, required string) (skipped bool, err error) {
	normalizedRequired := normalizeVersion(required)
	if !semver.IsValid(normalizedRequired) {
		return false, fmt.Errorf("invalid required version format: %s", normalizedRequired)
	}

	if current == "dev" {
		return false, nil
	}

	if IsUnstable(current) {
		return true, nil
	}

	normalizedCurrent := normalizeVersion(current)
	if !semver.IsValid(normalizedCurrent) {
		return false, fmt.Errorf("invalid current version format: %s", normalizedCurrent)
	}

	if semver.Compare(normalizedCurrent, normalizedRequired) < 0 {
		return false, fmt.Errorf(
			"this config requires datamitsu %s or higher. "+
				"Current version: %s. "+
				"Visit https://datamitsu.com/docs/getting-started/installation for upgrade instructions",
			normalizedRequired, normalizedCurrent,
		)
	}

	return false, nil
}

func normalizeVersion(v string) string {
	if v == "dev" {
		return "v0.0.0"
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}
