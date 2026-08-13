package binmanager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/ui"
	"go.uber.org/zap"
)

// MaxBinarySize is the maximum allowed download size (500 MiB).
const MaxBinarySize = 500 * 1024 * 1024

// Download retry policy: flaky mirrors (e.g. unofficial-builds.nodejs.org) and
// transient TLS/connection blips fail individual attempts; a bounded retry with
// exponential backoff turns those into success instead of failing the whole
// build. The parent context (install timeout) still bounds total time. The
// classification and backoff schedule live in internal/httpretry, shared with
// the OCI blob pull path.
const downloadMaxAttempts = httpretry.DefaultMaxAttempts

// Thin aliases over the shared retry policy keep this file's call sites and
// the existing tests unchanged.
func permanent(err error) error            { return httpretry.Permanent(err) }
func isPermanent(err error) bool           { return httpretry.IsPermanent(err) }
func retryableStatus(code int) bool        { return httpretry.RetryableStatus(code) }
func retryDelay(attempt int) time.Duration { return httpretry.Delay(attempt) }

// httpClient downloads binaries on the shared hardened transport (proxy, dialer,
// TLS/response-header timeouts, and the HTTPS→HTTP downgrade-rejecting redirect
// guard). It has NO end-to-end deadline: a flat budget encodes a hidden
// "size/speed" assumption that kills large-but-healthy downloads on slow
// links (a 400 MiB archive on a 1 Mbps VPN needs ~55 minutes). Stalls are
// caught by the per-attempt progress guard in downloadFileInternal instead.
var httpClient = httpx.NewHardenedClient(0)

// fileScheme marks a local-file source. It exists for the development loop:
// pointing a config at a locally built artifact (a freshly compiled WASM parser
// module, say) instead of a published release, without weakening anything — the
// caller hashes and verifies a file: source exactly like a downloaded one, so
// the mandatory-SHA-256 policy holds unchanged. Only the transport differs.
//
// Two independent locks gate it, so a released binary has no reachable
// local-read path at all:
//
//  1. **Build**: ldflags.LocalArtifacts must be injected (only the dev-link
//     build does this). Released and ordinary `go build` binaries refuse
//     file: outright.
//  2. **Call site**: the caller must pass allowLocalFile. Only the parser store
//     does, so an app, archive or JAR declaration can never reach it.
const fileScheme = "file://"

// maxLocalSourceSize bounds a file:// read, mirroring MaxBinarySize. A variable
// so tests can shrink it without writing half a gigabyte.
var maxLocalSourceSize int64 = MaxBinarySize

// copyLocalSource materializes a file: URL as a temp file in destDir, mirroring
// what a download leaves behind so the shared verify/publish path is identical.
// Failures are permanent: a missing or unreadable local file will not fix itself
// on retry. Offline mode does not apply — nothing leaves the machine.
func copyLocalSource(url, destDir string) (string, error) {
	srcPath := strings.TrimPrefix(url, fileScheme)
	if !filepath.IsAbs(srcPath) {
		return "", permanent(fmt.Errorf("file:// source must be an absolute path, got %q", srcPath))
	}
	// Regular files only. A FIFO or character device (/dev/zero, /dev/stdin)
	// would either block forever or stream until the size ceiling — a hang or a
	// wasted 500 MiB, neither of which a build artifact ever is.
	//
	// The check has to happen twice. O_NONBLOCK keeps the open itself from
	// blocking on a FIFO with no writer (opening one is what blocks, so a
	// post-open check would never be reached), and the fstat on the resulting
	// descriptor — not on the path — is what actually decides, so swapping the
	// path after the open cannot slip a pipe past it.
	src, err := os.OpenFile(srcPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", permanent(fmt.Errorf("open local source: %w", err))
	}
	info, err := src.Stat()
	if err != nil {
		_ = src.Close()
		return "", permanent(fmt.Errorf("stat local source: %w", err))
	}
	if !info.Mode().IsRegular() {
		_ = src.Close()
		return "", permanent(fmt.Errorf("file:// source must be a regular file, got mode %s", info.Mode()))
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			log.Warn("failed to close local source", zap.String("path", srcPath), zap.Error(closeErr))
		}
	}()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	tmpFile, err := os.CreateTemp(destDir, "local-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Same size ceiling as a download: a local path is still untrusted input.
	written, copyErr := io.Copy(tmpFile, io.LimitReader(src, maxLocalSourceSize+1))
	closeErr := tmpFile.Close()
	if copyErr == nil && written > maxLocalSourceSize {
		copyErr = fmt.Errorf("local source exceeds max size of %d bytes", maxLocalSourceSize)
	}
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after local copy error", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", permanent(fmt.Errorf("copy local source: %w", copyErr))
	}

	log.Debug("local source copied", zap.String("src", srcPath), zap.String("tmp", tmpPath))
	return tmpPath, nil
}

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

func downloadFileInternal(ctx context.Context, url string, destDir string, name string, allowLocalFile bool) (string, error) {
	if strings.HasPrefix(url, fileScheme) {
		if !ldflags.LocalArtifactsEnabled() {
			return "", permanent(fmt.Errorf(
				"file:// sources are not supported by this build (%s); they exist only in a dev-link build",
				displayName(name, url)))
		}
		if !allowLocalFile {
			return "", permanent(fmt.Errorf("file:// sources are not allowed for %s", displayName(name, url)))
		}
		return copyLocalSource(url, destDir)
	}

	if err := httpx.GuardOffline("download of " + displayName(name, url)); err != nil {
		return "", permanent(err)
	}

	// Bound the transfer by PROGRESS, not by an end-to-end deadline: only a
	// connection that delivers zero bytes for the whole window is aborted
	// (and then retried by the caller's retry loop).
	guard, ctx := httpx.NewStallGuard(ctx, httpx.DefaultStallWindow)
	defer guard.Stop()
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
		statusErr := fmt.Errorf("bad status: %s", resp.Status)
		if retryableStatus(resp.StatusCode) {
			return "", statusErr
		}
		return "", permanent(statusErr)
	}

	if resp.ContentLength > MaxBinarySize {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after size rejection", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", permanent(fmt.Errorf("file too large: Content-Length %d exceeds maximum %d bytes", resp.ContentLength, MaxBinarySize))
	}

	// LimitReader with MaxBinarySize+1 allows us to distinguish a file that is
	// exactly MaxBinarySize (written == MaxBinarySize, allowed) from one that
	// exceeds it (written > MaxBinarySize, rejected).
	limitedReader := guard.Reader(io.LimitReader(resp.Body, MaxBinarySize+1))

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
		if guard.Stalled() {
			return "", fmt.Errorf("download of %s stalled: no data received for %s", displayName(name, url), guard.Window())
		}
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	if written > MaxBinarySize {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove oversized temp file", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", permanent(fmt.Errorf("file too large: exceeded maximum %d bytes", MaxBinarySize))
	}

	log.Debug("file downloaded",
		zap.String("path", tmpPath),
		zap.Int64("size", written),
	)

	return tmpPath, nil
}

func downloadFile(ctx context.Context, url string, destDir string) (string, error) {
	return downloadFileInternal(ctx, url, destDir, "", false)
}

// downloadAndVerifyInternal downloads and hash-verifies a file, retrying
// transient download failures (network errors, 5xx) with exponential backoff. It
// stops early on a permanent error (4xx, oversized, hash mismatch) or once the
// parent context is done. The hash is verified after every successful download,
// so retries only re-fetch — they never weaken verification.
func downloadAndVerifyInternal(ctx context.Context, url string, expectedHash string, hashType BinHashType, destDir string, name string, allowLocalFile bool) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		tmpPath, err := downloadFileInternal(ctx, url, destDir, name, allowLocalFile)
		if err == nil {
			if vErr := verifyFileHash(tmpPath, expectedHash, hashType); vErr != nil {
				if removeErr := os.Remove(tmpPath); removeErr != nil {
					log.Warn("failed to remove temp file after hash verification failure", zap.String("path", tmpPath), zap.Error(removeErr))
				}
				// A complete body with the wrong hash is a genuine mismatch (wrong file
				// or stale hash), not a transient blip — re-downloading won't fix it. A
				// truncated transfer instead surfaces as a retryable io.Copy error before
				// the hash is ever checked.
				return "", fmt.Errorf("hash verification failed: %w", vErr)
			}
			log.Debug("file hash verified", zap.String("path", tmpPath))
			return tmpPath, nil
		}
		lastErr = err

		// Stop immediately on unfixable failures or once the parent context (e.g.
		// the install timeout) is cancelled — further attempts can't succeed.
		if ctx.Err() != nil || isPermanent(err) {
			return "", err
		}
		if attempt == downloadMaxAttempts {
			break
		}

		delay := retryDelay(attempt)
		log.Warn("download failed, retrying",
			zap.String("url", url),
			zap.Int("attempt", attempt),
			zap.Int("maxAttempts", downloadMaxAttempts),
			zap.Duration("delay", delay),
			zap.Error(err),
		)
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("download cancelled: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	return "", fmt.Errorf("after %d attempts: %w", downloadMaxAttempts, lastErr)
}

func downloadAndVerifyWithName(ctx context.Context, url string, expectedHash string, hashType BinHashType, destDir string, name string) (string, error) {
	return downloadAndVerifyInternal(ctx, url, expectedHash, hashType, destDir, name, false)
}

// DownloadAndVerifySHA256 downloads url into destDir (retrying transient
// failures with backoff) and verifies its SHA-256 hash, returning the path of
// the verified temp file. It is the minimal seam other store managers (e.g.
// parsermanager) reuse instead of re-implementing the hardened download+verify
// path. SHA-256 is enforced per the security policy; the caller is responsible
// for moving the verified file to its content-addressed location.
//
// allowLocalFile additionally accepts a `file://` URL, reading a local artifact
// instead of fetching one. The hash is still mandatory and verified identically,
// so this changes the transport, not the trust model; it exists for the
// build-locally development loop. Leave it false unless a store has a reason.
func DownloadAndVerifySHA256(ctx context.Context, url, expectedHash, destDir, name string, allowLocalFile bool) (string, error) {
	return downloadAndVerifyInternal(ctx, url, expectedHash, BinHashTypeSHA256, destDir, name, allowLocalFile)
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
