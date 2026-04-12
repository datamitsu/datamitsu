package detector

import "regexp"

// LibcPattern represents a libc variant detection pattern
type LibcPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// LibcPatterns maps libc names to their detection patterns in asset filenames
var LibcPatterns = map[string]*LibcPattern{
	"musl": {
		Name:    "musl",
		Pattern: regexp.MustCompile(`(?i)(musl|alpine)`),
	},
	"glibc": {
		Name:    "glibc",
		Pattern: regexp.MustCompile(`(?i)(gnu|glibc)`),
	},
}

// MatchLibc checks if filename matches a specific libc pattern.
// Returns true if the filename contains indicators for the given libc type.
func MatchLibc(filename string, libcType string) bool {
	pattern, ok := LibcPatterns[libcType]
	if !ok {
		return false
	}

	return pattern.Pattern.MatchString(filename)
}

// DetectLibcFromFilename detects which libc variant a filename indicates.
// Returns "musl", "glibc", or "" (empty) if no libc indicator found.
func DetectLibcFromFilename(filename string) string {
	if LibcPatterns["musl"].Pattern.MatchString(filename) {
		return "musl"
	}
	if LibcPatterns["glibc"].Pattern.MatchString(filename) {
		return "glibc"
	}
	return ""
}
