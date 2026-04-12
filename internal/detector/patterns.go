package detector

import "strings"

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
