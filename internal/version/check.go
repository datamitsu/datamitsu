package version

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

func CompareVersions(current, required string) error {
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
