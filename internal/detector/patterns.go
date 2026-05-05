package detector

import "strings"

// NonExecutableExtensions are file extensions for non-executable package formats to skip
var NonExecutableExtensions = []string{
	".vsix",  // VS Code extension
	".deb",   // Debian package
	".rpm",   // RPM package
	".nupkg", // NuGet package
	".whl",   // Python wheel
}

// IsNonExecutableFile checks if filename is a non-executable package format
func IsNonExecutableFile(filename string) bool {
	lowerName := strings.ToLower(filename)
	for _, ext := range NonExecutableExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}
	return false
}

// ChecksumExtensions are file extensions to skip (checksum files)
var ChecksumExtensions = []string{
	".sha256",
	".sha256sum",
	".sha512",
	".sha512sum",
	".md5",
	".md5sum",
	".checksum",
	".checksums",
	".txt", // Often used for checksums
}

// IsChecksumFile checks if filename is a checksum file
func IsChecksumFile(filename string) bool {
	lowerName := strings.ToLower(filename)

	for _, ext := range ChecksumExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}

	if strings.Contains(lowerName, "checksum") || strings.Contains(lowerName, "hash") {
		return true
	}

	return false
}
