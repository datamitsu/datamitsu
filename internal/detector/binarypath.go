// Package detector selects the best-matching release asset and binary path for
// a target OS, architecture, and libc using scoring and filename heuristics.
package detector

import (
	"cmp"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// DetectBinaryPath attempts to determine the binary path within an archive
// Uses simple heuristics - if uncertain, returns nil for manual completion
func DetectBinaryPath(appName string, filename string, contentType binmanager.BinContentType, osType syslist.OsType) *string {
	return DetectBinaryPathWithHistory(appName, filename, contentType, osType, "", "", nil)
}

// DetectBinaryPathWithHistory attempts to determine the binary path within an
// archive, learning the layout from the entries an earlier release of the same
// app recorded for this OS.
func DetectBinaryPathWithHistory(
	appName string,
	filename string,
	contentType binmanager.BinContentType,
	osType syslist.OsType,
	archType syslist.ArchType,
	libc string,
	historicalBinaries binmanager.MapOfBinaries,
) *string {
	// For non-archive types, no binary path needed
	if contentType == binmanager.BinContentTypeBinary || contentType == binmanager.BinContentTypeGz {
		return nil
	}

	if historical := historicalBinaryPath(historicalBinaries, osType, archType, libc, filename); historical != nil {
		return historical
	}

	return detectBinaryPathHeuristic(appName, filename, osType)
}

func detectBinaryPathHeuristic(appName string, filename string, osType syslist.OsType) *string {
	// Common patterns to try (in order of likelihood)
	patterns := []string{
		appName,                 // Direct: "appName"
		"bin/" + appName,        // In bin directory: "bin/appName"
		appName + "/" + appName, // Nested: "appName/appName"
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
		versionedPatterns := make([]string, 0, 2+len(patterns))
		versionedPatterns = append(versionedPatterns,
			appName+"-"+version+"/"+appName, // "appName-v1.2.3/appName"
			appName+"_"+version+"/"+appName, // "appName_v1.2.3/appName"
		)
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

// historicalEntry is one binary an earlier release recorded: the asset it
// came from, the path it named inside that asset, and the platform it served.
type historicalEntry struct {
	asset string
	path  string
	arch  syslist.ArchType
	libc  string
}

// historicalBinaryPath adapts a path recorded for an earlier release of the
// app to the new asset. An asset published under the same name as before keeps
// its layout. Otherwise the closest entry lends its layout — the same
// os/arch/libc first, then the same os/arch, then the same OS — with the new
// asset's name or version substituted. A borrowed layout that names another
// architecture or libc than the asset is skipped: it was recorded for a
// different asset, and taking it is how darwin/amd64 came to point into the
// aarch64 directory.
func historicalBinaryPath(history binmanager.MapOfBinaries, osType syslist.OsType, archType syslist.ArchType, libc, filename string) *string {
	var entries []historicalEntry
	for arch, libcMap := range history[osType] {
		for entryLibc, info := range libcMap {
			if info.BinaryPath == nil {
				continue
			}
			entries = append(entries, historicalEntry{
				asset: assetName(info.URL),
				path:  *info.BinaryPath,
				arch:  arch,
				libc:  entryLibc,
			})
		}
	}
	closeness := func(e historicalEntry) int {
		switch {
		case e.arch == archType && e.libc == libc:
			return 0
		case e.arch == archType:
			return 1
		default:
			return 2
		}
	}
	slices.SortFunc(entries, func(a, b historicalEntry) int {
		return cmp.Or(
			cmp.Compare(closeness(a), closeness(b)),
			cmp.Compare(a.path, b.path),
			cmp.Compare(a.arch, b.arch),
			cmp.Compare(a.libc, b.libc),
		)
	})

	for _, e := range entries {
		if e.asset == filename {
			return &e.path
		}
	}
	for _, e := range entries {
		derived := derivePath(e, filename)
		if pathContradictsAsset(derived, archType, libc, filename) {
			continue
		}
		return &derived
	}
	return nil
}

// derivePath adapts the path an earlier asset recorded to the new asset name.
// A path component that repeats the old asset's stem, as in
// tombi-cli-1.5.0-x86_64-unknown-linux-gnu/tombi, becomes the new stem, which
// carries version, architecture and libc at once. Otherwise only a version
// found in the path is replaced, and a path without one is kept as it is.
func derivePath(entry historicalEntry, filename string) string {
	oldStem, newStem := archiveStem(entry.asset), archiveStem(filename)
	if oldStem != "" && oldStem != newStem {
		parts := strings.Split(entry.path, "/")
		if i := slices.Index(parts, oldStem); i >= 0 {
			parts[i] = newStem
			return strings.Join(parts, "/")
		}
	}

	newVersion := extractVersion(filename)
	oldVersion, oldPart := extractVersionFromPath(entry.path)
	if newVersion == "" || oldVersion == "" || oldVersion == newVersion {
		return entry.path
	}
	newPart := strings.Replace(oldPart, oldVersion, newVersion, 1)
	return strings.Replace(entry.path, oldPart, newPart, 1)
}

// pathContradictsAsset reports whether a layout borrowed from another entry
// names an architecture other than the requested one, or a libc other than
// the asset's own (or, for an asset that names none, the requested one). Each
// path component is read without its version, so the digits of "tool-1.386.0"
// are not an architecture, and a libc token counts only where an architecture
// or Linux is named beside it, the way a target triple does: "gnu" or "musl"
// in a directory an application named after itself says nothing about the
// platform.
func pathContradictsAsset(path string, archType syslist.ArchType, libc, filename string) bool {
	assetLibc := DetectLibcFromFilename(filename)
	for part := range strings.SplitSeq(path, "/") {
		token := withoutVersion(part)
		for arch := range ArchPatterns {
			if arch != archType && MatchArch(token, arch) {
				return true
			}
		}
		pathLibc := DetectLibcFromFilename(token)
		if pathLibc == "" || (!HasAnyArchIndicator(token) && !linuxTokenPattern.MatchString(token)) {
			continue
		}
		switch {
		case assetLibc != "":
			if pathLibc != assetLibc {
				return true
			}
		case libc == "glibc" || libc == "musl":
			if pathLibc != libc {
				return true
			}
		}
	}
	return false
}

// linuxTokenPattern names Linux without naming a libc: the OS pattern also
// accepts "alpine" and "musl", which cannot corroborate themselves.
var linuxTokenPattern = regexp.MustCompile(`(?i)(linux|ubuntu)`)

// withoutVersion removes the version a path component carries.
func withoutVersion(part string) string {
	if version := extractVersionFromString(part); version != "" {
		return strings.Replace(part, version, "", 1)
	}
	return part
}

func assetName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return ""
	}
	return path.Base(u.Path)
}

// archiveSuffixes are the archive extensions an asset name can carry, the
// compound ones first so ".tar.gz" is stripped whole.
var archiveSuffixes = []string{".tar.bz2", ".tar.zst", ".tar.gz", ".tar.xz", ".tgz", ".txz", ".tbz", ".tar", ".zip"}

// archiveStem returns the asset name without its archive extension.
func archiveStem(filename string) string {
	lower := strings.ToLower(filename)
	for _, suffix := range archiveSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return filename[:len(filename)-len(suffix)]
		}
	}
	return filename
}

// extractVersionFromPath extracts version string from a path
// Returns both the version and the part it was found in
func extractVersionFromPath(path string) (string, string) {
	parts := strings.SplitSeq(path, "/")
	for part := range parts {
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
		for i := range len(part) {
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
	return extractVersionFromString(archiveStem(filename))
}

// isDigit checks if byte is a digit
func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
