package ocibundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ocidigest"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// blobSource abstracts where verified bundle bytes come from: a registry
// (store seed / auto-seed) or a local OCI image layout (store import). Every
// implementation returns only digest-verified content.
type blobSource interface {
	// manifest returns the raw manifest/index bytes pinned by digest,
	// verified as sha256(bytes) == digest.
	manifest(ctx context.Context, digest string) ([]byte, error)
	// blob materializes the blob pinned by the descriptor into a temp file
	// under destDir (digest- and size-verified) and returns its path. The
	// caller owns the file.
	blob(ctx context.Context, desc ocispec.Descriptor, destDir, displayName string) (string, error)
}

// registrySource pulls from an OCI registry, caching manifest bodies on disk
// by digest — the content is immutable, so the cache is eternal and repeated
// partial pulls of the same digest never fetch the manifest again.
type registrySource struct {
	resolver *ocidigest.Resolver
	repo     string
}

func newRegistrySource(host, repo string) *registrySource {
	return &registrySource{resolver: ocidigest.NewResolverForHost(host), repo: repo}
}

// manifestCachePath places cached manifest bodies under {cache}/oci/manifests.
func manifestCachePath(digest string) string {
	return filepath.Join(env.GetCachePath(), "oci", "manifests", strings.ReplaceAll(digest, ":", "-")+".json")
}

func (s *registrySource) manifest(ctx context.Context, digest string) ([]byte, error) {
	cachePath := manifestCachePath(digest)
	if data, err := os.ReadFile(cachePath); err == nil {
		if verifySHA256(data, digest) == nil {
			return data, nil
		}
		// A corrupted cache entry is replaced by a fresh verified pull.
		_ = os.Remove(cachePath)
	}

	data, err := s.resolver.PullManifest(ctx, s.repo, digest)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		tmp, tmpErr := os.CreateTemp(filepath.Dir(cachePath), ".manifest-*")
		if tmpErr == nil {
			if _, wErr := tmp.Write(data); wErr == nil && tmp.Close() == nil {
				_ = os.Rename(tmp.Name(), cachePath)
			} else {
				_ = tmp.Close()
				_ = os.Remove(tmp.Name())
			}
		}
	}
	return data, nil
}

func (s *registrySource) blob(ctx context.Context, desc ocispec.Descriptor, destDir, displayName string) (string, error) {
	return s.resolver.PullBlob(ctx, s.repo, desc.Digest.String(), desc.Size, maxCompressedBlobBytes, displayName, destDir)
}

// layoutSource reads a standard OCI image layout directory (`store import`):
// blobs live under blobs/sha256/<hex> and are verified on read, so a tampered
// layout fails exactly like a tampered registry.
type layoutSource struct {
	dir string
}

func newLayoutSource(dir string) *layoutSource { return &layoutSource{dir: dir} }

func (s *layoutSource) blobPath(digest string) (string, error) {
	hexPart, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(hexPart) != 64 {
		return "", fmt.Errorf("unsupported digest %q in OCI layout (only sha256)", digest)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("malformed digest %q in OCI layout", digest)
	}
	return filepath.Join(s.dir, "blobs", "sha256", hexPart), nil
}

func (s *layoutSource) manifest(_ context.Context, digest string) ([]byte, error) {
	p, err := s.blobPath(digest)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s from OCI layout: %w", digest, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, int64(manifestMaxBytesLayout)+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest %s from OCI layout: %w", digest, err)
	}
	if len(data) > manifestMaxBytesLayout {
		return nil, fmt.Errorf("manifest %s in OCI layout exceeds the %d byte limit", digest, manifestMaxBytesLayout)
	}
	if err := verifySHA256(data, digest); err != nil {
		return nil, fmt.Errorf("OCI layout manifest %s: %w", digest, err)
	}
	return data, nil
}

const manifestMaxBytesLayout = 4 << 20

func (s *layoutSource) blob(_ context.Context, desc ocispec.Descriptor, destDir, _ string) (path string, retErr error) {
	src, err := s.blobPath(desc.Digest.String())
	if err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("read blob %s from OCI layout: %w", desc.Digest, err)
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create blob temp directory: %w", err)
	}
	out, err := os.CreateTemp(destDir, "oci-blob-*")
	if err != nil {
		return "", fmt.Errorf("create blob temp file: %w", err)
	}
	tmpPath := out.Name()
	defer func() {
		if cErr := out.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
		if retErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, hasher), io.LimitReader(in, maxCompressedBlobBytes+1))
	if err != nil {
		return "", fmt.Errorf("copy blob %s from OCI layout: %w", desc.Digest, err)
	}
	if written > maxCompressedBlobBytes {
		return "", fmt.Errorf("blob %s exceeds the %d byte limit", desc.Digest, maxCompressedBlobBytes)
	}
	if desc.Size >= 0 && written != desc.Size {
		return "", fmt.Errorf("blob size mismatch for %s: expected %d bytes got %d", desc.Digest, desc.Size, written)
	}
	wantHex := strings.TrimPrefix(desc.Digest.String(), "sha256:")
	if got := hex.EncodeToString(hasher.Sum(nil)); got != wantHex {
		return "", fmt.Errorf("blob digest mismatch for %s: got sha256:%s", desc.Digest, got)
	}
	return tmpPath, nil
}

// verifySHA256 checks data against a "sha256:<hex>" digest string.
func verifySHA256(data []byte, digest string) error {
	wantHex, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return fmt.Errorf("unsupported digest %q (only sha256)", digest)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantHex {
		return fmt.Errorf("digest mismatch: expected sha256:%s got sha256:%s", wantHex, got)
	}
	return nil
}
