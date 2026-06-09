// Package ocibundle seeds the datamitsu store from an OCI bundle: a (possibly
// multi-platform) image whose layers carry per-subtree store content,
// annotated with the com.datamitsu.* layer/manifest annotations. The bundle
// is a cache accelerator and an airgap seed — never a trust boundary by
// itself: every blob is verified against its sha256 descriptor at pull time,
// and re-verifiable artifacts (single-file binaries, JVM jars) are
// additionally re-hashed against the published SHA-256 from the effective
// config after layout.
//
// The package only consumes bundles. Producing them is delegated to the
// community toolchain (buildx + regctl/oras/cosign); the annotation scheme
// below is the format contract between the two sides.
package ocibundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/logger"

	"go.uber.org/zap"
)

// Layer/manifest annotation keys shared with bundle producers.
const (
	// AnnotationSubtree marks a layer with the store-relative subtree it
	// carries (e.g. ".bin/golangci-lint/<hash>"). Layers without it are base
	// rootfs/config layers of the runnable image and are skipped.
	AnnotationSubtree = "com.datamitsu.subtree"
	// AnnotationKind is the optional layer content kind: binary|runtime|app|uv-python.
	AnnotationKind = "com.datamitsu.kind"
	// AnnotationApp optionally names the app/runtime a layer belongs to.
	AnnotationApp = "com.datamitsu.app"
	// AnnotationLibc distinguishes glibc/musl child manifests of one index.
	AnnotationLibc = "com.datamitsu.libc"
	// AnnotationStoreRoot is the absolute store root at build time, needed by
	// the relocating extractor for prefix rewriting (buildx images: /dm/store).
	AnnotationStoreRoot = "com.datamitsu.store-root"
)

var log = logger.Logger.With(zap.Namespace("ocibundle"))

// subtreeRoots are the only store subtrees a bundle layer may write into.
var subtreeRoots = []string{".bin/", ".runtimes/", ".apps/", ".uv/python"}

// validateSubtree rejects malformed subtree annotations: absolute paths,
// traversal, or anything outside the known store subtree roots. A layer with
// a malformed annotation is fatal (unlike a missing one, which means "not
// store content").
func validateSubtree(subtree string) error {
	if subtree == "" {
		return fmt.Errorf("empty %s annotation", AnnotationSubtree)
	}
	if strings.HasPrefix(subtree, "/") || filepath.IsAbs(subtree) {
		return fmt.Errorf("subtree %q must be store-relative", subtree)
	}
	cleaned := path.Clean(subtree)
	if cleaned != subtree {
		return fmt.Errorf("subtree %q is not a clean path", subtree)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return fmt.Errorf("subtree %q escapes the store", subtree)
	}
	for _, root := range subtreeRoots {
		if subtree == strings.TrimSuffix(root, "/") || strings.HasPrefix(subtree, root) {
			return nil
		}
	}
	return fmt.Errorf("subtree %q is outside the known store roots (%s)", subtree, strings.Join(subtreeRoots, ", "))
}

// Bundle layer media types accepted by the seeder: the OCI layer types plus
// the legacy docker variants that buildx-built images carry. Anything else is
// refused.
const (
	mediaTypeOCILayerGzip    = "application/vnd.oci.image.layer.v1.tar+gzip"
	mediaTypeOCILayerZstd    = "application/vnd.oci.image.layer.v1.tar+zstd"
	mediaTypeDockerLayerGzip = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	mediaTypeDockerLayerZstd = "application/vnd.docker.image.rootfs.diff.tar.zstd"
)

type layerCompression int

const (
	compressionGzip layerCompression = iota
	compressionZstd
)

func layerCompressionFor(mediaType string) (layerCompression, error) {
	switch mediaType {
	case mediaTypeOCILayerGzip, mediaTypeDockerLayerGzip:
		return compressionGzip, nil
	case mediaTypeOCILayerZstd, mediaTypeDockerLayerZstd:
		return compressionZstd, nil
	default:
		return 0, fmt.Errorf("unsupported layer media type %q", mediaType)
	}
}

// seedMarkerDir is the store-relative directory holding full-pull markers.
// Living inside the store, markers are removed by `store clear` together with
// the content they describe — no stale trust survives a wipe.
const seedMarkerDir = ".oci-seeded"

type seedMarker struct {
	Ref      string    `json:"ref"`
	Digest   string    `json:"digest"`
	Date     time.Time `json:"date"`
	Subtrees []string  `json:"subtrees"`
}

// markerPath converts the digest into a filesystem-safe marker file path.
func markerPath(storeRoot, digest string) string {
	return filepath.Join(storeRoot, seedMarkerDir, strings.ReplaceAll(digest, ":", "-"))
}

func markerExists(storeRoot, digest string) bool {
	_, err := os.Stat(markerPath(storeRoot, digest))
	return err == nil
}

// writeMarker records a completed FULL pull. Demand-driven pulls never write
// it — their idempotence is the per-subtree stat.
func writeMarker(storeRoot, ref, digest string, subtrees []string) error {
	data, err := json.MarshalIndent(seedMarker{
		Ref:      ref,
		Digest:   digest,
		Date:     time.Now().UTC(),
		Subtrees: subtrees,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal seed marker: %w", err)
	}
	dir := filepath.Join(storeRoot, seedMarkerDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create seed marker directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".marker-*")
	if err != nil {
		return fmt.Errorf("create seed marker temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write seed marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close seed marker: %w", err)
	}
	if err := os.Rename(tmpPath, markerPath(storeRoot, digest)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("place seed marker: %w", err)
	}
	return nil
}
