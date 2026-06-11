package ocibundle

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/target"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ErrNoPlatformMatch reports that the bundle index has no entry for the host
// (os, arch, libc). Online callers warn and fall through to the network; under
// offline it is fatal. Match with errors.Is.
var ErrNoPlatformMatch = errors.New("no matching platform in bundle index")

// Index media types (OCI index + legacy docker manifest list).
const (
	mediaTypeOCIIndex          = "application/vnd.oci.image.index.v1+json"
	mediaTypeDockerManifestLst = "application/vnd.docker.distribution.manifest.list.v2+json"
)

func isIndexMediaType(mediaType string) bool {
	return mediaType == mediaTypeOCIIndex || mediaType == mediaTypeDockerManifestLst
}

// selectDescriptor picks the child manifest matching the host from a bundle
// index: os/arch via the standard platform object (variant-tolerant — arm64
// entries may or may not carry "v8"), libc via the com.datamitsu.libc
// descriptor annotation (the standard platform object cannot express it; the
// annotation lives inside the digest-verified index bytes, so selecting by it
// is as tamper-proof as selecting by os/arch). On linux with an unknown host
// libc and no DATAMITSU_LIBC override no entry is guessed — the caller warns
// and falls through to the network.
func selectDescriptor(idx ocispec.Index, host target.Target) (ocispec.Descriptor, error) {
	var available []string
	for _, desc := range idx.Manifests {
		if desc.Platform == nil {
			continue
		}
		// buildx attaches provenance/SBOM attestation manifests with an
		// unknown/unknown platform — never store content.
		if desc.Platform.OS == "unknown" || desc.Platform.Architecture == "unknown" {
			continue
		}
		available = append(available, describePlatform(desc))

		if desc.Platform.OS != host.OS || desc.Platform.Architecture != host.Arch {
			continue
		}

		if host.OS != "linux" {
			return desc, nil
		}

		libc := desc.Annotations[AnnotationLibc]
		if libc == "" {
			// A linux entry without the libc annotation does not satisfy the
			// producer contract; never guess what it was built against.
			continue
		}
		if host.Libc == target.LibcUnknown {
			// Do not guess between glibc and musl: a wrong guess poisons the
			// store with binaries that cannot run.
			continue
		}
		if libc == string(host.Libc) {
			return desc, nil
		}
	}

	sort.Strings(available)
	detail := "index has no platform entries"
	if len(available) > 0 {
		detail = "index provides: " + strings.Join(available, ", ")
	}
	if host.OS == "linux" && host.Libc == target.LibcUnknown {
		detail += " (host libc detection failed; set DATAMITSU_LIBC=glibc or DATAMITSU_LIBC=musl)"
	}
	return ocispec.Descriptor{}, fmt.Errorf("%w for %s: %s", ErrNoPlatformMatch, host.String(), detail)
}

func describePlatform(desc ocispec.Descriptor) string {
	s := desc.Platform.OS + "/" + desc.Platform.Architecture
	if libc := desc.Annotations[AnnotationLibc]; libc != "" {
		s += "/" + libc
	}
	return s
}
