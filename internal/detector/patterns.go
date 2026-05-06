package detector

import "strings"

var nonExecutableExtensions = []string{
	".vsix",  // VS Code extension
	".deb",   // Debian package
	".rpm",   // RPM package
	".nupkg", // NuGet package
	".whl",   // Python wheel
	".msi",   // Windows installer
}

// IsNonExecutableFile checks if filename is a non-executable package format
func IsNonExecutableFile(filename string) bool {
	lowerName := strings.ToLower(filename)
	for _, ext := range nonExecutableExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}
	return false
}

var checksumExtensions = []string{
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

	for _, ext := range checksumExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}

	if strings.Contains(lowerName, "checksum") || strings.Contains(lowerName, "hash") {
		return true
	}

	return false
}
