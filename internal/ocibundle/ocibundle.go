// Package ocibundle seeds the datamitsu store from an OCI bundle: a (possibly
// multi-platform) image whose layers carry per-subtree store content,
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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/prepare"

	"go.uber.org/zap"
)

// Layer/manifest annotation keys shared with bundle producers.
const (
	// AnnotationSubtree marks a layer with the store-relative subtree it
	// carries (e.g. ".bin/golangci-lint/<hash>"). Layers without it are base
	// rootfs/config layers of the runnable image and are skipped.
	AnnotationSubtree = "com.datamitsu.subtree"
	// AnnotationKind is the optional layer content kind: binary|runtime|app|python-data|parser|prepared.
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

const (
	parsersSubtreeRoot  = ".parsers/"
	preparedSubtreeRoot = ".prepared/"
	appReadyName        = ".datamitsu-app-ready"
	appReadyContent     = "runtime-data-v1\n"
	pythonReadyName     = ".datamitsu-python-ready.json"
)

type pythonReadyRecord struct {
	Schema     int    `json:"schema"`
	Executable string `json:"executable"`
}

func subtreeReady(storeRoot, subtree string) bool {
	dest := filepath.Join(storeRoot, filepath.FromSlash(subtree))
	switch {
	case strings.HasPrefix(subtree, ".prepared/"):
		return prepare.Ready(dest)
	case strings.HasPrefix(subtree, ".apps/"):
		return appTreeReady(dest)
	case strings.HasPrefix(subtree, ".runtime-data/"):
		return pythonTreeReady(subtree, dest)
	default:
		_, err := os.Stat(dest)
		return err == nil
	}
}

func appTreeReady(appRoot string) bool {
	rootInfo, err := os.Lstat(appRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	markerInfo, err := os.Lstat(filepath.Join(appRoot, appReadyName))
	if err != nil || !markerInfo.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(filepath.Join(appRoot, appReadyName))
	return err == nil && string(data) == appReadyContent
}

func pythonTreeReady(subtree, pythonRoot string) bool {
	markerInfo, err := os.Lstat(filepath.Join(pythonRoot, pythonReadyName))
	if err != nil || !markerInfo.Mode().IsRegular() {
		return false
	}
	rootInfo, err := os.Lstat(pythonRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	data, err := os.ReadFile(filepath.Join(pythonRoot, pythonReadyName))
	if err != nil {
		return false
	}
	var record pythonReadyRecord
	if json.Unmarshal(data, &record) != nil || record.Schema != 1 || record.Executable == "" {
		return false
	}
	prefix := subtree + "/"
	if !strings.HasPrefix(record.Executable, prefix) || path.Clean(record.Executable) != record.Executable {
		return false
	}
	rel := strings.TrimPrefix(record.Executable, prefix)
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return false
	}
	executable := filepath.Join(pythonRoot, filepath.FromSlash(rel))
	rootResolved, err := filepath.EvalSymlinks(pythonRoot)
	if err != nil {
		return false
	}
	executableResolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	contained, err := filepath.Rel(rootResolved, executableResolved)
	if err != nil || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return false
	}
	info, err := os.Stat(executableResolved)
	return err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0)
}

func validTargetName(s string) bool {
	if len(s) == 0 || len(s) > 64 || !isASCIIAlphaNum(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isASCIIAlphaNum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func isASCIIAlphaNum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func validIdentity(s string) bool {
	if len(s) != 32 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validateSubtree(subtree string) error {
	if subtree == "" {
		return fmt.Errorf("empty %s annotation", AnnotationSubtree)
	}
	if strings.HasPrefix(subtree, "/") || filepath.IsAbs(subtree) {
		return fmt.Errorf("subtree %q must be store-relative", subtree)
	}
	if path.Clean(subtree) != subtree {
		return fmt.Errorf("subtree %q is not a clean path", subtree)
	}
	parts := strings.Split(subtree, "/")
	valid := false
	switch {
	case len(parts) == 3 && (parts[0] == ".bin" || parts[0] == ".runtimes"):
		valid = validTargetName(parts[1]) && validIdentity(parts[2])
	case len(parts) == 4 && parts[0] == ".apps":
		valid = (parts[1] == "bun" || parts[1] == "go" || parts[1] == "jvm" || parts[1] == "node" || parts[1] == "uv") && validTargetName(parts[2]) && validIdentity(parts[3])
	case len(parts) == 3 && (parts[0] == ".parsers" || parts[0] == ".prepared"):
		valid = validTargetName(parts[1]) && validIdentity(parts[2])
	case len(parts) == 4 && parts[0] == ".runtime-data":
		valid = validTargetName(parts[1]) && validIdentity(parts[2]) && parts[3] == "python"
	}
	if !valid {
		return fmt.Errorf("subtree %q is not an exact owned store target", subtree)
	}
	return nil
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

// markerExists reports whether a full pull of this digest ever completed. It
// answers "was the marker written", not "is it still true" — see
// markerSatisfied for the question seeding asks.
func markerExists(storeRoot, digest string) bool {
	_, err := os.Stat(markerPath(storeRoot, digest))
	return err == nil
}

// markerSatisfied reports whether a completed full pull of this digest still
// describes the store. The marker records the subtrees it laid down, so this
// can be checked rather than assumed: if any of them has since gone — a manual
// rm, a partially restored cache, a store the core has since laid out
// differently — the marker is stale and the pull must run again.
//
// Bare existence was the previous check, which made `store seed` a silent
// no-op in precisely the situation that prompts someone to re-run it. Only
// subtrees this bundle actually recorded are checked, so a partial bundle (a
// deliberate, supported case) never triggers an endless re-pull.
func markerSatisfied(storeRoot, digest string) bool {
	data, err := os.ReadFile(markerPath(storeRoot, digest))
	if err != nil {
		return false
	}
	var marker seedMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		log.Debug("discarding unreadable seed marker", zap.String("digest", digest), zap.Error(err))
		return false
	}
	for _, subtree := range marker.Subtrees {
		// subtreeReady, not a bare stat: a recorded prepared state whose
		// marker was deleted is an incomplete store, exactly like a missing
		// subtree — re-running the full seed must repair it, and `store
		// status` must not call the store fully seeded.
		if !subtreeReady(storeRoot, subtree) {
			log.Debug("seed marker is stale: recorded subtree is missing or incomplete",
				zap.String("digest", digest), zap.String("subtree", subtree))
			return false
		}
	}
	return true
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
