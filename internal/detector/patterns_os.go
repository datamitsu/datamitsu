package detector

import (
	"github.com/datamitsu/datamitsu/internal/syslist"
	"regexp"
)

// OSPattern represents OS detection pattern
type OSPattern struct {
	Name            syslist.OsType
	Pattern         *regexp.Regexp
	AntiPattern     *regexp.Regexp
	PriorityPattern *regexp.Regexp
}

// OSPatterns maps OS types to their detection patterns
var OSPatterns = map[syslist.OsType]*OSPattern{
	syslist.OsTypeDarwin: {
		Name:        syslist.OsTypeDarwin,
		Pattern:     regexp.MustCompile(`(?i)(darwin|macos|osx|mac[\s_-]?os|[_-]mac[_-])`),
		AntiPattern: regexp.MustCompile(`(?i)(ios)`),
	},
	syslist.OsTypeLinux: {
		Name:            syslist.OsTypeLinux,
		Pattern:         regexp.MustCompile(`(?i)(linux|ubuntu)`),
		AntiPattern:     regexp.MustCompile(`(?i)(android)`),
		PriorityPattern: regexp.MustCompile(`(?i)\.appimage$`),
	},
	syslist.OsTypeWindows: {
		Name:            syslist.OsTypeWindows,
		Pattern:         regexp.MustCompile(`(?i)(windows|win64|win32|msvc|mingw)`),
		PriorityPattern: regexp.MustCompile(`(?i)\.exe$`),
	},
	syslist.OsTypeFreebsd: {
		Name:    syslist.OsTypeFreebsd,
		Pattern: regexp.MustCompile(`(?i)(freebsd)`),
	},
	syslist.OsTypeOpenbsd: {
		Name:    syslist.OsTypeOpenbsd,
		Pattern: regexp.MustCompile(`(?i)(openbsd)`),
	},
}

// MatchOS checks if filename matches the OS pattern
func MatchOS(filename string, osType syslist.OsType) bool {
	pattern, ok := OSPatterns[osType]
	if !ok {
		return false
	}

	if pattern.AntiPattern != nil && pattern.AntiPattern.MatchString(filename) {
		return false
	}

	return pattern.Pattern.MatchString(filename)
}

// HasPriorityPattern checks if filename matches OS priority pattern
func HasPriorityPattern(filename string, osType syslist.OsType) bool {
	pattern, ok := OSPatterns[osType]
	if !ok || pattern.PriorityPattern == nil {
		return false
	}

	return pattern.PriorityPattern.MatchString(filename)
}
