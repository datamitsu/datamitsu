package detector

import (
	"regexp"

	"github.com/datamitsu/datamitsu/internal/syslist"
)

// ArchPattern represents architecture detection pattern
type ArchPattern struct {
	Name    syslist.ArchType
	Pattern *regexp.Regexp
}

// ArchPatterns maps the architectures datamitsu can run on (and therefore
// select) to their detection patterns. Variant spellings matter: separators
// (aarch_64, x86-64), "NNbit" forms (64bit, 32bit) and the Windows-only
// "win64" all appear in the wild.
// Architectures datamitsu does NOT target but which still appear in release
// assets are matched by ForeignArchPattern instead — both feed
// HasAnyArchIndicator() so the implicit-amd64 fallback in scoring.go never
// claims a non-amd64 asset.
var ArchPatterns = map[syslist.ArchType]*ArchPattern{
	syslist.ArchTypeAmd64: {
		Name:    syslist.ArchTypeAmd64,
		Pattern: regexp.MustCompile(`(?i)(x64|amd64|x86[\s_-]?64|64[\s_-]?bit|win64)`),
	},
	syslist.ArchTypeArm64: {
		Name:    syslist.ArchTypeArm64,
		Pattern: regexp.MustCompile(`(?i)(arm64|armv8|aarch[\s_-]?64)`),
	},
	syslist.ArchType386: {
		Name:    syslist.ArchType386,
		Pattern: regexp.MustCompile(`(?i)(x32|i?386|i686|x86[\s_-]?32|32[\s_-]?bit)`),
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

// ForeignArchPattern matches architecture tokens for targets datamitsu does not
// build for (loongarch, mips, sparc, ppc, s390, riscv, wasm, big-endian arm).
// It exists solely so HasAnyArchIndicator() recognises these assets and the
// implicit-amd64 fallback excludes them rather than misclassifying e.g.
// just-…-loongarch64-…musl.tar.gz as amd64. Word boundaries keep it from firing
// on amd64/arm64 names. Never used for selection — only as an indicator.
//
// "win32" is here too: on its own it names a 32-bit build, which must not take
// the amd64 slot the way protoc-36.2-win32.zip did, but Electron-style names
// use it for Windows itself ("app-win32-x64"), so it cannot select 386 either.
var ForeignArchPattern = regexp.MustCompile(
	`(?i)(\bloong\w*|\bmips\w*|\bsparc\w*|\bs390\w*|\briscv\w*|\bppc\w*|\bpowerpc\w*|\bwasm\b|\barmbe\b|\barm64be\b|(?:^|[^a-z0-9])win32(?:$|[^a-z0-9]))`,
)

// MatchArch checks if filename matches the architecture pattern
func MatchArch(filename string, archType syslist.ArchType) bool {
	pattern, ok := ArchPatterns[archType]
	if !ok {
		return false
	}

	return pattern.Pattern.MatchString(filename)
}
