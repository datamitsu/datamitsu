package binmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/datamitsu/datamitsu/internal/httpretry"
)

// VerifyRetryNotifier is told about every repeated verification download, so
// a command can show what it is waiting for. Nil retries silently.
var VerifyRetryNotifier httpretry.Notifier

// DownloadError is a verification that never got the asset: the network or
// the server failed, not the asset. A caller choosing between candidates must
// not judge the candidate by it.
type DownloadError struct {
	Err error
}

func (e *DownloadError) Error() string { return "download failed: " + e.Err.Error() }

// Unwrap exposes the wrapped error for errors.Is/As chains.
func (e *DownloadError) Unwrap() error { return e.Err }

// IsDownloadError reports whether err comes from fetching an asset rather than
// from what the asset holds.
func IsDownloadError(err error) bool {
	var download *DownloadError
	return errors.As(err, &download)
}

// downloadForVerify fetches url for a verification, retrying transient
// failures under the shared policy and reporting each retry through
// VerifyRetryNotifier. A failure is a DownloadError.
func downloadForVerify(ctx context.Context, url, destDir string) (string, error) {
	var path string
	err := httpretry.Retry(ctx, "GET "+url, VerifyRetryNotifier, func() error {
		downloaded, err := downloadFile(ctx, url, destDir)
		if err != nil {
			return err
		}
		path = downloaded
		return nil
	})
	if err != nil {
		return "", &DownloadError{Err: err}
	}
	return path, nil
}

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
	if hash == "" {
		return errors.New("hash is empty: verification requires a non-empty hash")
	}

	tempDir, err := os.MkdirTemp("", "datamitsu-verify-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	downloadedPath, err := downloadForVerify(ctx, url, tempDir)
	if err != nil {
		return err
	}

	if err := verifyFileHash(downloadedPath, hash, hashType); err != nil {
		return fmt.Errorf("hash verification failed: %w", err)
	}

	extractedPath, err := extractBinary(downloadedPath, contentType, binaryPath, tempDir)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	return CheckExtractedExecutable(extractedPath)
}

// CheckExtractedExecutable verifies that an extracted file — a single binary, or the binaryPath
// inside an extracted directory — exists, is not empty and is in a format an OS can execute.
func CheckExtractedExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("extracted file not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("extracted path %q is a directory, not a file", path)
	}
	if info.Size() == 0 {
		return errors.New("extracted file is empty")
	}
	return checkExecutableFormat(path)
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

// DownloadFileForVerify downloads a file to destDir for verify-all, retrying
// transient failures; a failure is a DownloadError.
func DownloadFileForVerify(ctx context.Context, url string, destDir string) (string, error) {
	return downloadForVerify(ctx, url, destDir)
}

// VerifyFileHashPublic verifies a file's hash. Public wrapper around verifyFileHash for verify-all.
func VerifyFileHashPublic(filePath string, expectedHash string, hashType BinHashType) error {
	return verifyFileHash(filePath, expectedHash, hashType)
}

// ExtractDirForVerify extracts an archive to a directory. Public wrapper around extractBinaryToDir for verify-all.
func ExtractDirForVerify(archivePath string, contentType BinContentType, destDir string) (string, error) {
	return extractBinaryToDir(archivePath, contentType, destDir)
}
