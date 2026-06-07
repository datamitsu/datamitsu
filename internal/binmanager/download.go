package binmanager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/ui"
	"go.uber.org/zap"
)

// MaxBinarySize is the maximum allowed download size (500 MiB).
const MaxBinarySize = 500 * 1024 * 1024

// httpClient downloads binaries on the shared hardened transport (proxy, dialer,
// TLS/response-header timeouts, and the HTTPS→HTTP downgrade-rejecting redirect
// guard). The 5-minute budget accommodates large archive downloads.
var httpClient = httpx.NewHardenedClient(5 * time.Minute)

// displayName returns a human-friendly label for a download, falling back to
// the URL's last path segment when no explicit name is given.
func displayName(name, url string) string {
	if name != "" {
		return name
	}
	if i := strings.LastIndexByte(url, '/'); i >= 0 && i+1 < len(url) {
		return url[i+1:]
	}
	return url
}

func downloadFileInternal(ctx context.Context, url string, destDir string, name string) (string, error) {
	if name != "" {
		log.Debug("downloading file", zap.String("url", url), zap.String("name", name))
	} else {
		log.Debug("downloading file", zap.String("url", url))
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(destDir, "download-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if err := tmpFile.Close(); err != nil {
			log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after request build error", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("failed to build download request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after download error", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after bad status", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	if resp.ContentLength > MaxBinarySize {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after size rejection", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("file too large: Content-Length %d exceeds maximum %d bytes", resp.ContentLength, MaxBinarySize)
	}

	// LimitReader with MaxBinarySize+1 allows us to distinguish a file that is
	// exactly MaxBinarySize (written == MaxBinarySize, allowed) from one that
	// exceeds it (written > MaxBinarySize, rejected).
	limitedReader := io.LimitReader(resp.Body, MaxBinarySize+1)

	// Route the transfer through the process-wide display: in an interactive
	// terminal this renders a progress bar in the shared container, in CI/pipe
	// it emits throttled percentage lines, and when no display is active it is
	// a transparent passthrough.
	reader := ui.Current().Download(displayName(name, url), resp.ContentLength, limitedReader)
	defer func() {
		if err := reader.Close(); err != nil {
			log.Warn("failed to close progress reader", zap.Error(err))
		}
	}()

	written, err := io.Copy(tmpFile, reader)
	if err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after write error", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	if written > MaxBinarySize {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove oversized temp file", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("file too large: exceeded maximum %d bytes", MaxBinarySize)
	}

	log.Debug("file downloaded",
		zap.String("path", tmpPath),
		zap.Int64("size", written),
	)

	return tmpPath, nil
}

func downloadFile(ctx context.Context, url string, destDir string) (string, error) {
	return downloadFileInternal(ctx, url, destDir, "")
}

func downloadAndVerifyInternal(ctx context.Context, url string, expectedHash string, hashType BinHashType, destDir string, name string) (string, error) {
	tmpPath, err := downloadFileInternal(ctx, url, destDir, name)
	if err != nil {
		return "", err
	}

	if err := verifyFileHash(tmpPath, expectedHash, hashType); err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after hash verification failure", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("hash verification failed: %w", err)
	}

	log.Debug("file hash verified", zap.String("path", tmpPath))

	return tmpPath, nil
}

func downloadAndVerify(ctx context.Context, url string, expectedHash string, hashType BinHashType, destDir string) (string, error) {
	return downloadAndVerifyInternal(ctx, url, expectedHash, hashType, destDir, "")
}

func downloadAndVerifyWithName(ctx context.Context, url string, expectedHash string, hashType BinHashType, destDir string, name string) (string, error) {
	return downloadAndVerifyInternal(ctx, url, expectedHash, hashType, destDir, name)
}

func moveFile(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Content-addressed dst: if it already exists, the content is identical, so
	// skip the move entirely. This avoids ever removing a live dst (closes the
	// ENOENT window under concurrent installs of the same binary).
	if _, err := os.Stat(dst); err == nil {
		_ = os.Remove(src)
		log.Debug("destination already exists, skipping move", zap.String("dst", dst))
		return nil
	}

	if err := os.Chmod(src, 0o755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	// os.Rename is atomic on POSIX and replaces an existing dst in place, so a
	// concurrent move that won the race is never observed as a missing dst.
	if err := os.Rename(src, dst); err != nil {
		// Cross-device fallback: copy into a temp file in the destination
		// directory, then atomically rename it into place. Copying directly onto
		// dst would expose a partial, non-executable file to concurrent readers —
		// the exact ENOENT/exec-format window this move is meant to close.
		if copyErr := copyFileAtomic(src, dst); copyErr != nil {
			return fmt.Errorf("rename failed: %w, and copy fallback failed: %w", err, copyErr)
		}
		_ = os.Remove(src)
	}

	log.Debug("file moved and permissions set", zap.String("dst", dst))

	return nil
}

func moveDir(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("failed to create destination parent directory: %w", err)
	}

	// Content-addressed dst: if it already exists, another install already placed
	// identical content there. Skip rather than RemoveAll-then-Rename, which would
	// briefly expose a missing dst to a concurrent reader.
	if _, err := os.Stat(dst); err == nil {
		_ = os.RemoveAll(src)
		log.Debug("destination directory already exists, skipping move", zap.String("dst", dst))
		return nil
	}

	if err := os.Rename(src, dst); err != nil {
		// A concurrent move may have created dst between our Stat and Rename. If so,
		// treat it as success (content-addressed, identical content).
		if _, statErr := os.Stat(dst); statErr == nil {
			_ = os.RemoveAll(src)
			log.Debug("destination directory created concurrently, skipping move", zap.String("dst", dst))
			return nil
		}
		// Cross-device fallback: copy into a temp dir in the destination parent,
		// then atomically rename it into place. Copying directly onto dst would
		// expose a partially-populated directory to concurrent readers — the
		// exact window this move is meant to close.
		if copyErr := copyDirAtomic(src, dst); copyErr != nil {
			return fmt.Errorf("rename failed: %w, and copy fallback failed: %w", err, copyErr)
		}
		_ = os.RemoveAll(src)
	}

	log.Debug("directory moved", zap.String("dst", dst))
	return nil
}

// copyDirAtomic copies src into a temp directory in dst's parent, then
// atomically renames it onto dst. Unlike copyDir, dst is never observed in a
// partially-populated state: concurrent readers see either the absent dst or
// the fully-copied one. If a concurrent move created dst first, the rename
// fails but dst is already complete, so it is treated as success.
func copyDirAtomic(src, dst string) error {
	tmpDir, err := os.MkdirTemp(filepath.Dir(dst), "move-*")
	if err != nil {
		return fmt.Errorf("create temp dir for %q: %w", dst, err)
	}
	// Always clean up the temp dir: on success it is empty (staged was renamed
	// out), and on the concurrent-dst path it still holds the staged copy.
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// copyDir creates dst itself, so copy into a fresh child of the temp dir.
	staged := filepath.Join(tmpDir, "staged")
	if err := copyDir(src, staged); err != nil {
		return err
	}
	if err := os.Rename(staged, dst); err != nil {
		if _, statErr := os.Stat(dst); statErr == nil {
			return nil
		}
		return fmt.Errorf("rename %q to %q: %w", staged, dst, err)
	}
	return nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read dir %q: %w", src, err)
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("read symlink %q: %w", srcPath, err)
			}
			if err := os.Symlink(target, dstPath); err != nil {
				return fmt.Errorf("create symlink %q: %w", dstPath, err)
			}
		case entry.IsDir():
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		default:
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) (retErr error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %q: %w", src, err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	defer func() {
		if cErr := srcFile.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
	}()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("open %q for writing: %w", dst, err)
	}
	defer func() {
		if cErr := dstFile.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
	}()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy %q to %q: %w", src, dst, err)
	}
	return nil
}

// copyFileAtomic copies src into a temp file in dst's directory, marks it
// executable, then atomically renames it onto dst. Unlike copyFile, dst is
// never observed in a partial state: concurrent readers see either the old
// (absent) dst or the fully-written, executable one.
func copyFileAtomic(src, dst string) (retErr error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), "move-*")
	if err != nil {
		return fmt.Errorf("create temp file for %q: %w", dst, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err := copyFile(src, tmpName); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file %q: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("rename %q to %q: %w", tmpName, dst, err)
	}
	return nil
}
