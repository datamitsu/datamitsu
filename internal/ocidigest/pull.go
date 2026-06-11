package ocidigest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/ui"

	"go.uber.org/zap"
)

// manifestMaxBytes caps a pulled manifest/index body. Real manifests are a few
// KiB; 4 MiB leaves room for annotation-heavy indexes while still bounding a
// hostile registry response.
const manifestMaxBytes = 4 << 20

// ErrManifestNotFound reports a 404 for a digest-pinned manifest — typically a
// bundle whose untagged manifests were garbage-collected by the registry.
var ErrManifestNotFound = errors.New("manifest not found in registry")

// errDigestMismatch is the base of all content-vs-digest failures. They are
// permanent: re-downloading identical wrong bytes cannot help.
var errDigestMismatch = errors.New("digest mismatch")

// IsDigestMismatch reports whether err is a content-verification failure
// (manifest body or blob not matching its pinned digest/size).
func IsDigestMismatch(err error) bool {
	return errors.Is(err, errDigestMismatch)
}

// parseSHA256Digest validates a "sha256:<64 hex>" digest string and returns
// the hex tail.
func parseSHA256Digest(dgst string) (string, error) {
	hexPart, ok := strings.CutPrefix(dgst, "sha256:")
	if !ok {
		return "", fmt.Errorf("unsupported digest %q: only sha256 digests are accepted", dgst)
	}
	if len(hexPart) != 64 {
		return "", fmt.Errorf("malformed sha256 digest %q", dgst)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("malformed sha256 digest %q", dgst)
	}
	if hexPart != strings.ToLower(hexPart) {
		return "", fmt.Errorf("malformed sha256 digest %q: hex must be lowercase", dgst)
	}
	return hexPart, nil
}

// PullManifest fetches the manifest (or index) pinned by digest and returns
// its raw bytes after verifying sha256(body) == digest. Unlike Resolve, the
// Docker-Content-Digest header is never trusted here — the body itself is
// hashed, so not a single unverified byte reaches the caller.
func (r *Resolver) PullManifest(ctx context.Context, repo, dgst string) ([]byte, error) {
	wantHex, err := parseSHA256Digest(dgst)
	if err != nil {
		return nil, err
	}

	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", r.scheme, r.registry, repo, dgst)
	resp, err := r.authedGet(ctx, repo, manifestURL, acceptManifests)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s@%s (was the bundle garbage-collected? re-publish it or update oci.digest)", ErrManifestNotFound, repo, dgst)
	default:
		return nil, fmt.Errorf("registry returned status %d for manifest %s@%s", resp.StatusCode, repo, dgst)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, manifestMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest body for %s@%s: %w", repo, dgst, err)
	}
	if len(body) > manifestMaxBytes {
		return nil, fmt.Errorf("manifest %s@%s exceeds the %d byte limit", repo, dgst, manifestMaxBytes)
	}

	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != wantHex {
		return nil, fmt.Errorf("manifest body %w for %s: expected sha256:%s got sha256:%s", errDigestMismatch, repo, wantHex, got)
	}
	return body, nil
}

// PullBlob downloads the blob pinned by digest into a temp file under destDir,
// verifying both the SHA-256 of the streamed bytes and (when size >= 0) the
// byte count against the descriptor. Transient failures are retried with the
// shared backoff policy; a digest or size mismatch is permanent — identical
// wrong bytes cannot become right on retry. Returns the temp file path; the
// caller owns (and removes) it.
func (r *Resolver) PullBlob(ctx context.Context, repo, dgst string, size, maxBytes int64, displayName, destDir string) (string, error) {
	if _, err := parseSHA256Digest(dgst); err != nil {
		return "", err
	}
	if size >= 0 && size > maxBytes {
		return "", fmt.Errorf("blob %s declared size %d exceeds the %d byte limit", dgst, size, maxBytes)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create blob temp directory: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= httpretry.DefaultMaxAttempts; attempt++ {
		path, err := r.pullBlobOnce(ctx, repo, dgst, size, maxBytes, displayName, destDir)
		if err == nil {
			return path, nil
		}
		lastErr = err

		if ctx.Err() != nil || httpretry.IsPermanent(err) {
			return "", err
		}
		if attempt == httpretry.DefaultMaxAttempts {
			break
		}

		delay := httpretry.Delay(attempt)
		logger.Logger.Warn("blob download failed, retrying",
			zap.String("digest", dgst),
			zap.Int("attempt", attempt),
			zap.Duration("delay", delay),
			zap.Error(err),
		)
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("blob download cancelled: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	return "", fmt.Errorf("after %d attempts: %w", httpretry.DefaultMaxAttempts, lastErr)
}

func (r *Resolver) pullBlobOnce(ctx context.Context, repo, dgst string, size, maxBytes int64, displayName, destDir string) (path string, retErr error) {
	wantHex, err := parseSHA256Digest(dgst)
	if err != nil {
		return "", httpretry.Permanent(err)
	}

	tmpFile, err := os.CreateTemp(destDir, "oci-blob-*")
	if err != nil {
		return "", httpretry.Permanent(fmt.Errorf("create blob temp file: %w", err))
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if cErr := tmpFile.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
		if retErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	blobURL := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", r.scheme, r.registry, repo, dgst)
	// GHCR answers with a 307 to its CDN; the hardened client follows it and
	// net/http itself strips Authorization on the cross-host hop. The redirect
	// target is untrusted either way — trust rests on the digest check below.
	resp, err := r.authedGet(ctx, repo, blobURL, "")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("registry returned status %d for blob %s@%s", resp.StatusCode, repo, dgst)
		if httpretry.RetryableStatus(resp.StatusCode) {
			return "", statusErr
		}
		return "", httpretry.Permanent(statusErr)
	}

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxBytes+1)
	reader := ui.Current().Download(displayName, size, limited)
	defer func() { _ = reader.Close() }()

	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), reader)
	if err != nil {
		return "", fmt.Errorf("stream blob %s: %w", dgst, err)
	}
	if written > maxBytes {
		return "", httpretry.Permanent(fmt.Errorf("blob %s exceeds the %d byte limit", dgst, maxBytes))
	}
	if size >= 0 && written != size {
		return "", httpretry.Permanent(fmt.Errorf("blob size %w for %s: expected %d bytes got %d", errDigestMismatch, dgst, size, written))
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != wantHex {
		return "", httpretry.Permanent(fmt.Errorf("blob %w: expected sha256:%s got sha256:%s", errDigestMismatch, wantHex, got))
	}
	return tmpPath, nil
}
