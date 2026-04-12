package binmanager

import (
	"fmt"
	"os"
)

// VerifyBinaryExtraction downloads and verifies that a binary can be extracted successfully
// Returns nil if verification succeeds, error otherwise
func VerifyBinaryExtraction(
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

	downloadedPath, err := downloadFile(url, tempDir)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if hash == "" {
		return fmt.Errorf("hash is empty: verification requires a non-empty hash")
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
		return fmt.Errorf("extracted file is empty")
	}

	return nil
}

// DownloadFileForVerify downloads a file to destDir. Public wrapper around downloadFile for verify-all.
func DownloadFileForVerify(url string, destDir string) (string, error) {
	return downloadFile(url, destDir)
}

// VerifyFileHashPublic verifies a file's hash. Public wrapper around verifyFileHash for verify-all.
func VerifyFileHashPublic(filePath string, expectedHash string, hashType BinHashType) error {
	return verifyFileHash(filePath, expectedHash, hashType)
}

// ExtractDirForVerify extracts an archive to a directory. Public wrapper around extractBinaryToDir for verify-all.
func ExtractDirForVerify(archivePath string, contentType BinContentType, destDir string) (string, error) {
	return extractBinaryToDir(archivePath, contentType, destDir)
}
