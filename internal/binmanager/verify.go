package binmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// VerifyBinaryExtraction downloads and verifies that a binary can be extracted successfully
// Returns nil if verification succeeds, error otherwise
func VerifyBinaryExtraction(
	ctx context.Context,
	url string,
	hash string,
	hashType BinHashType,
	contentType BinContentType,
	binaryPath *string,
) error {
	tempDir, err := os.MkdirTemp("", "datamitsu-verify-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	downloadedPath, err := downloadFile(ctx, url, tempDir)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if hash == "" {
		return errors.New("hash is empty: verification requires a non-empty hash")
	}
	if err := verifyFileHash(downloadedPath, hash, hashType); err != nil {
		return fmt.Errorf("hash verification failed: %w", err)
	}

	extractedPath, err := extractBinary(downloadedPath, contentType, binaryPath, tempDir)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	info, err := os.Stat(extractedPath)
	if err != nil {
		return fmt.Errorf("extracted file not found: %w", err)
	}

	if info.Size() == 0 {
		return errors.New("extracted file is empty")
	}

	return checkExecutableFormat(extractedPath)
}

// executableMagics are the leading bytes of what an OS can execute: ELF,
// Mach-O (thin in either byte order, and universal), PE, and a "#!" script.
// Anything else — a completion script or a man page that a loose binaryPath
// picked out of an archive — fails at run time with "exec format error".
var executableMagics = [][]byte{
	{0x7f, 'E', 'L', 'F'},
	{0xfe, 0xed, 0xfa, 0xce},
	{0xce, 0xfa, 0xed, 0xfe},
	{0xfe, 0xed, 0xfa, 0xcf},
	{0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe},
	{0xca, 0xfe, 0xba, 0xbf},
	{'M', 'Z'},
	{'#', '!'},
}

func checkExecutableFormat(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open extracted file: %w", err)
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, 16)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("failed to read extracted file: %w", err)
	}
	head = head[:n]

	for _, magic := range executableMagics {
		if bytes.HasPrefix(head, magic) {
			return nil
		}
	}
	return fmt.Errorf("extracted file is not an executable (no ELF, Mach-O, PE or #! header), it starts with %q", head)
}

// DownloadFileForVerify downloads a file to destDir. Public wrapper around downloadFile for verify-all.
func DownloadFileForVerify(ctx context.Context, url string, destDir string) (string, error) {
	return downloadFile(ctx, url, destDir)
}

// VerifyFileHashPublic verifies a file's hash. Public wrapper around verifyFileHash for verify-all.
func VerifyFileHashPublic(filePath string, expectedHash string, hashType BinHashType) error {
	return verifyFileHash(filePath, expectedHash, hashType)
}

// ExtractDirForVerify extracts an archive to a directory. Public wrapper around extractBinaryToDir for verify-all.
func ExtractDirForVerify(archivePath string, contentType BinContentType, destDir string) (string, error) {
	return extractBinaryToDir(archivePath, contentType, destDir)
}
