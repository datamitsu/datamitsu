package detector

import (
	"regexp"

	"github.com/datamitsu/datamitsu/internal/syslist"
)

// OSPattern represents OS detection pattern
type OSPattern struct {
	Name            syslist.OsType
	Pattern         *regexp.Regexp
	AntiPattern     *regexp.Regexp
	PriorityPattern *regexp.Regexp
}

// OSPatterns maps OS types to their detection patterns. An Alpine or musl
// build is a Linux build, and some releases name nothing else: an asset like
// "tool-alpine" carries neither "linux" nor an arch token, and without an OS
// indicator neither implicit rule in ScoreAsset would claim it.
var OSPatterns = map[syslist.OsType]*OSPattern{
	syslist.OsTypeDarwin: {
		Name:        syslist.OsTypeDarwin,
		Pattern:     regexp.MustCompile(`(?i)(darwin|macos|osx|mac[\s_-]?os|[_-]mac[_-])`),
		AntiPattern: regexp.MustCompile(`(?i)(ios)`),
	},
	syslist.OsTypeLinux: {
		Name:            syslist.OsTypeLinux,
		Pattern:         regexp.MustCompile(`(?i)(linux|ubuntu|alpine|musl)`),
		AntiPattern:     regexp.MustCompile(`(?i)(android)`),
		PriorityPattern: regexp.MustCompile(`(?i)\.appimage$`),
	},
	syslist.OsTypeWindows: {
		// A ".exe" suffix names Windows on its own ("snyk-win.exe" carries no
		// other token), and so does a "win" token between separators — bounded
		// so "darwin" does not qualify.
		Name:            syslist.OsTypeWindows,
		Pattern:         regexp.MustCompile(`(?i)(windows|win64|win32|msvc|mingw|(?:^|[-_.])win(?:$|[-_.])|\.exe$)`),
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

// ForeignOSPattern matches operating systems datamitsu does not run on but
// which appear in release assets next to the ones it selects. It exists so
// HasAnyOSIndicator recognises them and the implicit-Linux rule in ScoreAsset
// never claims e.g. tombi-cli-1.5.5-x86_64-unknown-illumos.tar.gz for
// linux/amd64: the file is an ELF, so extraction verification passes, and it
// fails only at run time. Bounded by separators so "aix" cannot fire inside a
// longer word. Never used for selection — only as an indicator.
var ForeignOSPattern = regexp.MustCompile(
	`(?i)(?:^|[^a-z0-9])(illumos|solaris|sunos|netbsd|dragonfly|haiku|android|aix|plan9)(?:$|[^a-z0-9])`,
)

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
