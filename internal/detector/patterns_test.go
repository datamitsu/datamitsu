package detector

import (
	"github.com/datamitsu/datamitsu/internal/syslist"
	"testing"
)

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
		{"ARMv8 match", "binary-linux-armv8", syslist.ArchTypeArm64, true},
		{"386 match", "binary-linux-386", syslist.ArchType386, true},
		{"i386 match", "binary-linux-i386", syslist.ArchType386, true},
		{"x86_32 match", "binary-linux-x86_32", syslist.ArchType386, true},
		{"i686 match", "binary-linux-i686", syslist.ArchType386, true},
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
