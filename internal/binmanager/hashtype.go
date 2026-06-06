package binmanager

// BinHashType identifies the cryptographic hash algorithm used to verify a download.
type BinHashType string

const defaultBinHashType BinHashType = "sha256"

// Supported hash algorithms for verifying downloaded artifacts.
const (
	BinHashTypeSHA1   BinHashType = "sha1"
	BinHashTypeSHA256 BinHashType = "sha256"
	BinHashTypeSHA384 BinHashType = "sha384"
	BinHashTypeSHA512 BinHashType = "sha512"
	BinHashTypeMD5    BinHashType = "md5"
)

// IsAllowedDownloadHashType returns true if the hash type is allowed for download verification.
// Per security policy, all artifacts downloaded from the internet must use SHA-256.
func IsAllowedDownloadHashType(ht BinHashType) bool {
	return ht == BinHashTypeSHA256
}

// BinContentType identifies the on-disk format of a downloaded artifact.
type BinContentType string

// Supported content/archive formats for downloaded artifacts.
const (
	BinContentTypeBinary BinContentType = "binary"
	BinContentTypeTarGz  BinContentType = "tar.gz"
	BinContentTypeTarBz2 BinContentType = "tar.bz2"
	BinContentTypeTarXz  BinContentType = "tar.xz"
	BinContentTypeTarZst BinContentType = "tar.zst"
	BinContentTypeTar    BinContentType = "tar"
	BinContentTypeZip    BinContentType = "zip"
	BinContentTypeGz     BinContentType = "gz"
	BinContentTypeBz2    BinContentType = "bz2"
	BinContentTypeXz     BinContentType = "xz"
	BinContentTypeZst    BinContentType = "zst"
)

// BinaryOsArchInfo describes a downloadable binary for one OS/arch, including its
// source URL, verification hash and extraction details.
type BinaryOsArchInfo struct {
	URL      string       `json:"url"`
	Hash     string       `json:"hash"`
	HashType *BinHashType `json:"hashType,omitempty"`

	ContentType BinContentType `json:"contentType"`

	// Path to binary inside archive (if archive)
	// Example: "myapp-v1.0.0/bin/myapp" or just "myapp"
	BinaryPath *string `json:"binaryPath,omitempty"`

	// ExtractDir extracts the entire archive to a directory instead of a single binary.
	// Used for runtimes like JDK that need the full directory tree (bin/, lib/, etc.).
	ExtractDir bool `json:"extractDir,omitempty"`
}
