package detector

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/syslist"
)

func TestIsNonExecutableFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{"VSIX extension", "tool-linux-x64.vsix", true},
		{"DEB extension", "tool-linux-amd64.deb", true},
		{"RPM extension", "tool-linux-x86_64.rpm", true},
		{"NUPKG extension", "tool.nupkg", true},
		{"WHL extension", "package-1.0.0-py3-none-any.whl", true},
		{"MSI extension", "tool-windows-amd64.msi", true},
		{"Regular tar.gz", "tool-linux-amd64.tar.gz", false},
		{"Regular zip", "tool-windows-amd64.zip", false},
		{"Plain binary", "tool-linux-amd64", false},
		{"EXE binary", "tool-windows-amd64.exe", false},
		{"PKG extension", "tool-darwin-arm64.pkg", true},
		{"Case insensitive VSIX", "tool-linux-x64.VSIX", true},
		{"Case insensitive DEB", "tool-linux-amd64.DEB", true},
		{"Case insensitive PKG", "tool-darwin-arm64.PKG", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNonExecutableFile(tt.filename)
			if result != tt.expected {
				t.Errorf("IsNonExecutableFile(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestIsChecksumFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{"SHA256 extension", "file.sha256", true},
		{"SHA256SUM extension", "file.sha256sum", true},
		{"SHA512 extension", "file.sha512", true},
		{"MD5 extension", "file.md5", true},
		{"TXT extension", "file.txt", true},
		{"CHECKSUM extension", "file.checksum", true},
		{"Contains checksum", "some-checksums.list", true},
		{"Contains hash", "file-hashes.json", true},
		{"Regular binary", "binary-linux-amd64", false},
		{"Tar.gz archive", "archive.tar.gz", false},
		{"Exe file", "program.exe", false},
		{"Mixed case SHA256", "file.SHA256", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsChecksumFile(tt.filename)
			if result != tt.expected {
				t.Errorf("IsChecksumFile(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestMatchOS(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		osType   syslist.OsType
		expected bool
	}{
		{"Darwin match", "binary-darwin-amd64", syslist.OsTypeDarwin, true},
		{"MacOS match", "binary-macos-arm64", syslist.OsTypeDarwin, true},
		{"OSX match", "binary-osx-amd64", syslist.OsTypeDarwin, true},
		{"Linux match", "binary-linux-amd64", syslist.OsTypeLinux, true},
		{"Ubuntu match", "binary-ubuntu-20.04", syslist.OsTypeLinux, true},
		{"Windows match", "binary-windows-amd64.exe", syslist.OsTypeWindows, true},
		{"Win64 match", "binary-win64.exe", syslist.OsTypeWindows, true},
		{"FreeBSD match", "binary-freebsd-amd64", syslist.OsTypeFreebsd, true},
		{"OpenBSD match", "binary-openbsd-amd64", syslist.OsTypeOpenbsd, true},
		{"iOS anti-pattern", "binary-ios-arm64", syslist.OsTypeDarwin, false},
		{"Android anti-pattern", "binary-android-arm64", syslist.OsTypeLinux, false},
		{"No match", "binary-unknown-amd64", syslist.OsTypeDarwin, false},
		{"Case insensitive", "binary-DARWIN-amd64", syslist.OsTypeDarwin, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchOS(tt.filename, tt.osType)
			if result != tt.expected {
				t.Errorf("MatchOS(%q, %v) = %v, want %v", tt.filename, tt.osType, result, tt.expected)
			}
		})
	}
}

func TestMatchArch(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		archType syslist.ArchType
		expected bool
	}{
		{"AMD64 match", "binary-linux-amd64", syslist.ArchTypeAmd64, true},
		{"x64 match", "binary-linux-x64", syslist.ArchTypeAmd64, true},
		{"x86_64 match", "binary-linux-x86_64", syslist.ArchTypeAmd64, true},
		{"ARM64 match", "binary-darwin-arm64", syslist.ArchTypeArm64, true},
		{"aarch64 match", "binary-linux-aarch64", syslist.ArchTypeArm64, true},
		{"aarch_64 underscore match", "protoc-35.0-linux-aarch_64.zip", syslist.ArchTypeArm64, true},
		{"aarch-64 hyphen match", "tool-osx-aarch-64.zip", syslist.ArchTypeArm64, true},
		{"ARMv8 match", "binary-linux-armv8", syslist.ArchTypeArm64, true},
		{"64bit means amd64 not arm64", "trivy_0.71.0_Linux-64bit.tar.gz", syslist.ArchTypeArm64, false},
		{"64bit match amd64", "trivy_0.71.0_Linux-64bit.tar.gz", syslist.ArchTypeAmd64, true},
		{"64-bit hyphen match amd64", "vale_3.14.2_Linux_64-bit.tar.gz", syslist.ArchTypeAmd64, true},
		{"loongarch64 not amd64", "just-1.51.0-loongarch64-unknown-linux-musl.tar.gz", syslist.ArchTypeAmd64, false},
		{"386 match", "binary-linux-386", syslist.ArchType386, true},
		{"i386 match", "binary-linux-i386", syslist.ArchType386, true},
		{"x86_32 match", "binary-linux-x86_32", syslist.ArchType386, true},
		{"i686 match", "binary-linux-i686", syslist.ArchType386, true},
		{"32bit match 386", "trivy_0.71.0_Linux-32bit.tar.gz", syslist.ArchType386, true},
		{"32bit not amd64", "trivy_0.71.0_Linux-32bit.tar.gz", syslist.ArchTypeAmd64, false},
		{"ARM bare match", "binary-linux-arm", syslist.ArchTypeArm, true},
		{"ARM32 match", "binary-linux-arm32", syslist.ArchTypeArm, true},
		{"ARMv7 match", "binary-linux-armv7", syslist.ArchTypeArm, true},
		{"ARMhf match", "binary-linux-armhf", syslist.ArchTypeArm, true},
		{"ARM underscore delimited", "tool_linux_arm.tar.gz", syslist.ArchTypeArm, true},
		{"ARM underscore both sides", "tool_arm_v6.tar.gz", syslist.ArchTypeArm, true},
		{"ARM hyphen delimited", "tool-linux-arm.tar.gz", syslist.ArchTypeArm, true},
		{"ARM at end of name", "tool-arm", syslist.ArchTypeArm, true},
		{"ARM at start", "arm-tool", syslist.ArchTypeArm, true},
		{"ARM not in charm", "charm-linux-amd64", syslist.ArchTypeArm, false},
		{"ARM not in farm", "farm-tool-linux-x64", syslist.ArchTypeArm, false},
		{"ARM not in armadillo", "armadillo-linux-amd64", syslist.ArchTypeArm, false},
		{"ppc64le match", "binary-linux-ppc64le", syslist.ArchTypePpc64le, true},
		{"powerpc64le match", "binary-linux-powerpc64le", syslist.ArchTypePpc64le, true},
		{"s390x match", "binary-linux-s390x", syslist.ArchTypeS390x, true},
		{"riscv64 match", "binary-linux-riscv64", syslist.ArchTypeRiscv64, true},
		{"No match", "binary-linux-unknown", syslist.ArchTypeAmd64, false},
		{"Case insensitive", "binary-linux-AMD64", syslist.ArchTypeAmd64, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchArch(tt.filename, tt.archType)
			if result != tt.expected {
				t.Errorf("MatchArch(%q, %v) = %v, want %v", tt.filename, tt.archType, result, tt.expected)
			}
		})
	}
}

func TestHasAnyArchIndicator(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		// Targets datamitsu selects.
		{"amd64", "tool-linux-amd64.tar.gz", true},
		{"arm64", "tool-linux-arm64.tar.gz", true},
		{"aarch_64 variant", "protoc-35.0-linux-aarch_64.zip", true},
		{"64bit variant", "trivy_0.71.0_Linux-64bit.tar.gz", true},
		{"32bit variant", "trivy_0.71.0_Linux-32bit.tar.gz", true},
		// Foreign targets datamitsu only recognises — must NOT fall through to
		// the implicit-amd64 rule in scoring.
		{"loongarch64", "just-1.51.0-loongarch64-unknown-linux-musl.tar.gz", true},
		{"loong64", "tool-linux-loong64", true},
		{"mips64le", "tool-linux-mips64le", true},
		{"mipsle", "tool-linux-mipsle", true},
		{"sparc64", "tool-linux-sparc64", true},
		{"s390", "tool-linux-s390", true},
		{"riscv", "tool-linux-riscv", true},
		{"ppc64", "tool-linux-ppc64", true},
		{"powerpc", "tool-linux-powerpc", true},
		{"wasm", "tool-wasm.tar.gz", true},
		// No arch token at all → false (lets implicit rules apply).
		{"no arch token", "tool-linux.tar.gz", false},
		{"plain name", "tool.tar.gz", false},
		// Word boundaries: foreign tokens must not fire mid-word (no \b, no other
		// arch token present).
		{"mips not mid-word in armips", "tool-armips.tar.gz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAnyArchIndicator(tt.filename); got != tt.expected {
				t.Errorf("HasAnyArchIndicator(%q) = %v, want %v", tt.filename, got, tt.expected)
			}
		})
	}
}

func TestHasPriorityPattern(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		osType   syslist.OsType
		expected bool
	}{
		{"Linux AppImage", "binary.appimage", syslist.OsTypeLinux, true},
		{"Windows EXE", "binary.exe", syslist.OsTypeWindows, true},
		{"Linux no priority", "binary-linux-amd64", syslist.OsTypeLinux, false},
		{"Windows no priority", "binary-windows-amd64", syslist.OsTypeWindows, false},
		{"Darwin no priority pattern", "binary-darwin-amd64", syslist.OsTypeDarwin, false},
		{"Case insensitive EXE", "binary.EXE", syslist.OsTypeWindows, true},
		{"Case insensitive AppImage", "binary.AppImage", syslist.OsTypeLinux, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasPriorityPattern(tt.filename, tt.osType)
			if result != tt.expected {
				t.Errorf("HasPriorityPattern(%q, %v) = %v, want %v", tt.filename, tt.osType, result, tt.expected)
			}
		})
	}
}

func TestMatchOSInvalidType(t *testing.T) {
	result := MatchOS("binary-something", syslist.OsType("invalid"))
	if result {
		t.Error("MatchOS should return false for invalid OS type")
	}
}

func TestMatchArchInvalidType(t *testing.T) {
	result := MatchArch("binary-something", syslist.ArchType("invalid"))
	if result {
		t.Error("MatchArch should return false for invalid arch type")
	}
}

func TestHasPriorityPatternInvalidType(t *testing.T) {
	result := HasPriorityPattern("binary.exe", syslist.OsType("invalid"))
	if result {
		t.Error("HasPriorityPattern should return false for invalid OS type")
	}
}
