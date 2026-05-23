package version

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// IsUnstable reports whether the given version string is an unstable build
// (release pipeline format: "0.0.0-unstable.<date>.<sha>"). Unstable builds
// are produced from non-tagged commits and are not subject to the config
// version check — callers that gate on version should warn rather than block.
func IsUnstable(v string) bool {
	return strings.Contains(v, "-unstable")
}

func CompareVersions(current, required string) error {
	if IsUnstable(current) {
		// Unstable builds sort below v0.0.0 under semver prerelease rules, so
		// any required version would fail the check. Skip enforcement; the
		// caller is expected to log an advisory warning.
		return nil
	}

	current = normalizeVersion(current)
	required = normalizeVersion(required)

	if !semver.IsValid(current) {
		return fmt.Errorf("invalid current version format: %s", current)
	}
	if !semver.IsValid(required) {
		return fmt.Errorf("invalid required version format: %s", required)
	}

	if semver.Compare(current, required) < 0 {
		return fmt.Errorf(
			"this config requires datamitsu %s or higher. "+
				"Current version: %s. "+
				"Run 'go install github.com/datamitsu/datamitsu@latest' to upgrade",
			required, current,
		)
	}

	return nil
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
