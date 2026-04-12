package detector

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"strings"
)

// DetectBinaryPath attempts to determine the binary path within an archive
// Uses simple heuristics - if uncertain, returns nil for manual completion
func DetectBinaryPath(appName string, filename string, contentType binmanager.BinContentType, osType syslist.OsType) *string {
	return DetectBinaryPathWithHistory(appName, filename, contentType, osType, nil)
}

// DetectBinaryPathWithHistory attempts to determine the binary path within an archive
// using historical data from previous versions of the same app
func DetectBinaryPathWithHistory(
	appName string,
	filename string,
	contentType binmanager.BinContentType,
	osType syslist.OsType,
	historicalBinaries binmanager.MapOfBinaries,
) *string {
	// For non-archive types, no binary path needed
	if contentType == binmanager.BinContentTypeBinary || contentType == binmanager.BinContentTypeGz {
		return nil
	}

	// Try to learn from historical data first
	if historicalPattern := extractBinaryPathPattern(historicalBinaries, osType, appName, filename); historicalPattern != nil {
		return historicalPattern
	}

	// Fallback to heuristic-based detection
	return detectBinaryPathHeuristic(appName, filename, osType)
}

func detectBinaryPathHeuristic(appName string, filename string, osType syslist.OsType) *string {
	// Common patterns to try (in order of likelihood)
	patterns := []string{
		appName,                    // Direct: "appName"
		"bin/" + appName,           // In bin directory: "bin/appName"
		appName + "/" + appName,    // Nested: "appName/appName"
	}

	// Add .exe extension for Windows
	if osType == syslist.OsTypeWindows {
		winPatterns := make([]string, 0, len(patterns))
		for _, p := range patterns {
			winPatterns = append(winPatterns, p+".exe")
		}
		patterns = winPatterns
	}

	// Try to extract version from filename and add version-based patterns
	if version := extractVersion(filename); version != "" {
		versionedPatterns := []string{
			appName + "-" + version + "/" + appName,  // "appName-v1.2.3/appName"
			appName + "_" + version + "/" + appName,  // "appName_v1.2.3/appName"
		}
		if osType == syslist.OsTypeWindows {
			for i := range versionedPatterns {
				versionedPatterns[i] += ".exe"
			}
		}
		patterns = append(versionedPatterns, patterns...)
	}

	if len(patterns) > 0 {
		return &patterns[0]
	}

	return nil
}

// extractBinaryPathPattern analyzes historical binaries for the same app
// and extracts a common pattern to use for new versions
func extractBinaryPathPattern(historicalBinaries binmanager.MapOfBinaries, osType syslist.OsType, appName string, filename string) *string {
	if len(historicalBinaries) == 0 {
		return nil
	}

	var paths []string
	var hasNilPath bool

	for os, archMap := range historicalBinaries {
		if os != osType {
			continue
		}
		for _, libcMap := range archMap {
			for _, binInfo := range libcMap {
				if binInfo.BinaryPath == nil {
					hasNilPath = true
				} else {
					paths = append(paths, *binInfo.BinaryPath)
				}
			}
		}
	}

	if hasNilPath && len(paths) == 0 {
		return nil
	}

	if len(paths) == 0 {
		return nil
	}

	commonPattern := findCommonPattern(paths, appName, filename)
	return commonPattern
}

// findCommonPattern finds a common pattern among historical paths
// and applies it to the new filename
func findCommonPattern(paths []string, appName string, newFilename string) *string {
	if len(paths) == 0 {
		return nil
	}

	newVersion := extractVersion(newFilename)
	if newVersion == "" {
		return nil
	}

	for _, path := range paths {
		oldVersion, oldPart := extractVersionFromPath(path)
		if oldVersion == "" {
			continue
		}

		if oldVersion == newVersion {
			return &path
		}

		newPart := strings.Replace(oldPart, oldVersion, newVersion, 1)
		pattern := strings.Replace(path, oldPart, newPart, 1)
		return &pattern
	}

	if len(paths) > 0 {
		return &paths[0]
	}

	return nil
}

// extractVersionFromPath extracts version string from a path
// Returns both the version and the part it was found in
func extractVersionFromPath(path string) (string, string) {
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if version := extractVersionFromString(part); version != "" {
			return version, part
		}
	}
	return "", ""
}

// extractVersionFromString extracts version from any string
// Recognizes patterns like "2.7.2", "v0.56.4", "1.2.3-beta"
func extractVersionFromString(s string) string {
	// Split only on hyphens and underscores, preserve dots for version numbers
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_'
	})

	for _, part := range parts {
		// Check for v-prefixed version (e.g., "v1.2.3")
		if strings.HasPrefix(part, "v") && len(part) > 1 && isDigit(part[1]) {
			// Extract the version part after 'v'
			version := part[1:]
			if isValidVersion(version) {
				return part
			}
		}

		// Check for non-prefixed version (e.g., "1.2.3")
		if len(part) > 0 && isDigit(part[0]) && isValidVersion(part) {
			return part
		}
	}

	return ""
}

// isValidVersion checks if a string looks like a semantic version
// Expects at least one dot and digits (e.g., "1.2", "2.7.2", "0.56.4")
func isValidVersion(s string) bool {
	if !strings.Contains(s, ".") {
		return false
	}

	// Split on dots and verify all parts are numeric
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}

	for _, part := range parts {
		if len(part) == 0 {
			return false
		}
		// Check if all characters are digits
		for i := 0; i < len(part); i++ {
			if !isDigit(part[i]) {
				return false
			}
		}
	}

	return true
}

// extractVersion attempts to extract version string from filename
// Returns empty string if no clear version found
func extractVersion(filename string) string {
	// Remove extension
	name := strings.TrimSuffix(filename, ".tar.gz")
	name = strings.TrimSuffix(name, ".tar.xz")
	name = strings.TrimSuffix(name, ".zip")
	name = strings.TrimSuffix(name, ".tgz")
	name = strings.TrimSuffix(name, ".txz")

	return extractVersionFromString(name)
}

// isDigit checks if byte is a digit
func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
