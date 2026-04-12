package binmanager

type BinHashType string

const defaultBinHashType BinHashType = "sha256"

const (
	BinHashTypeSHA1    BinHashType = "sha1"
	BinHashTypeSHA256  BinHashType = "sha256"
	BinHashTypeSHA384  BinHashType = "sha384"
	BinHashTypeSHA512  BinHashType = "sha512"
	BinHashTypeMD5 BinHashType = "md5"
)

// IsAllowedDownloadHashType returns true if the hash type is allowed for download verification.
// Per security policy, all artifacts downloaded from the internet must use SHA-256.
func IsAllowedDownloadHashType(ht BinHashType) bool {
	return ht == BinHashTypeSHA256
}

type BinContentType string

const (
	BinContentTypeBinary  BinContentType = "binary"
	BinContentTypeTarGz   BinContentType = "tar.gz"
	BinContentTypeTarBz2  BinContentType = "tar.bz2"
	BinContentTypeTarXz   BinContentType = "tar.xz"
	BinContentTypeTarZst  BinContentType = "tar.zst"
	BinContentTypeTar     BinContentType = "tar"
	BinContentTypeZip     BinContentType = "zip"
	BinContentTypeGz      BinContentType = "gz"
	BinContentTypeBz2     BinContentType = "bz2"
	BinContentTypeXz      BinContentType = "xz"
	BinContentTypeZst     BinContentType = "zst"
)

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
