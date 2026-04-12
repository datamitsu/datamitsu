package detector

import (
	"github.com/datamitsu/datamitsu/internal/syslist"
	"regexp"
)

// ArchPattern represents architecture detection pattern
type ArchPattern struct {
	Name    syslist.ArchType
	Pattern *regexp.Regexp
}

// ArchPatterns maps architecture types to their detection patterns.
// All architectures that appear in release assets must have entries here so
// that HasAnyArchIndicator() returns true and the implicit-amd64 fallback in
// scoring.go does not misclassify them.
var ArchPatterns = map[syslist.ArchType]*ArchPattern{
	syslist.ArchTypeAmd64: {
		Name:    syslist.ArchTypeAmd64,
		Pattern: regexp.MustCompile(`(?i)(x64|amd64|x86[\s_-]?64)`),
	},
	syslist.ArchTypeArm64: {
		Name:    syslist.ArchTypeArm64,
		Pattern: regexp.MustCompile(`(?i)(arm64|armv8|aarch64)`),
	},
	syslist.ArchType386: {
		Name:    syslist.ArchType386,
		Pattern: regexp.MustCompile(`(?i)(x32|i?386|i686|x86[\s_-]?32)`),
	},
	syslist.ArchTypeArm: {
		Name:    syslist.ArchTypeArm,
		Pattern: regexp.MustCompile(`(?i)(arm32|armv[67]|armhf|(?:^|[^a-zA-Z])arm(?:$|[^a-zA-Z0-9]))`),
	},
	syslist.ArchTypePpc64le: {
		Name:    syslist.ArchTypePpc64le,
		Pattern: regexp.MustCompile(`(?i)(ppc64le|powerpc64le)`),
	},
	syslist.ArchTypeS390x: {
		Name:    syslist.ArchTypeS390x,
		Pattern: regexp.MustCompile(`(?i)s390x`),
	},
	syslist.ArchTypeRiscv64: {
		Name:    syslist.ArchTypeRiscv64,
		Pattern: regexp.MustCompile(`(?i)riscv64`),
	},
}

// MatchArch checks if filename matches the architecture pattern
func MatchArch(filename string, archType syslist.ArchType) bool {
	pattern, ok := ArchPatterns[archType]
	if !ok {
		return false
	}

	return pattern.Pattern.MatchString(filename)
}
