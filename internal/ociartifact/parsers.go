// Package ociartifact pulls a datamitsu WASM parser module published as a
// standalone OCI artifact. It closes the last hole in the "mirror one registry
// and everything works" promise: the parser module was the one artifact that
// always came from github.com over HTTPS, so an air-gapped or
// registry-only organization could mirror every binary and still lose
// diagnostics parsing.
//
// The trust anchor is not the registry. It is the mandatory SHA-256 the config
// already declares for every parser: the artifact's single layer must have
// digest "sha256:"+hash, which makes the OCI digest chain and the config hash
// literally the same number. A registry serving a correctly-digested manifest
// that points at different content is rejected before one payload byte is
// requested.
//
// The package takes plain strings and never imports internal/config, so the
// config package stays free of the registry client, the progress UI and the
// HTTP stack.
package ociartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/ocidigest"
	"github.com/datamitsu/datamitsu/internal/ociref"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	// ArtifactTypeParsers is the manifest-level artifactType of a datamitsu
	// parser module artifact — a vendor-tree media type, which is what OCI
	// image-spec v1.1 expects for artifactType.
	ArtifactTypeParsers = "application/vnd.datamitsu.parsers.v1+wasm"

	// MediaTypeWasm is the only accepted layer media type, and the layer must be
	// UNCOMPRESSED: the whole design rests on the layer digest being the digest
	// of the .wasm file itself. A closed allowlist is affordable because we are
	// the sole publisher of this artifact type.
	MediaTypeWasm = "application/wasm"

	// MaxParserModuleBytes caps a parser module blob. The real module is about
	// 377 KiB; 64 MiB is generous headroom and far below binmanager's 500 MiB
	// binary cap or ocibundle's 2 GiB blob cap, neither of which is a sane
	// ceiling for a single wasm file.
	MaxParserModuleBytes int64 = 64 << 20

	// sha256Prefix is the digest algorithm prefix. Only sha256 is accepted, on
	// both the manifest pin and the layer, matching ocidigest.
	sha256Prefix = "sha256:"
)

// errIntegrity is the base of every artifact-policy violation: the bytes are
// authentic (they matched their digests) but the artifact is not shaped like a
// parser module, or its layer is not the module the config declares.
var errIntegrity = errors.New("oci artifact integrity")

// ErrModuleNotFound reports that the pinned artifact manifest is gone from the
// registry — typically an untagged manifest the registry garbage-collected.
var ErrModuleNotFound = errors.New("parser module artifact not found in registry")

// IsIntegrityError reports whether err is an artifact-policy violation. These
// are fatal by design: never retried (identical wrong bytes cannot become
// right) and never degraded to another source, because "fall back to the URL"
// is exactly the behaviour an air-gapped deployment must be able to rule out.
func IsIntegrityError(err error) bool { return errors.Is(err, errIntegrity) }

// registryClient is the narrow slice of *ocidigest.Resolver this package uses.
// It exists as a test seam: NewResolverForHost hardcodes the https scheme on an
// unexported field, so out-of-package code cannot point a real Resolver at an
// httptest server. Deliberately not solved with an insecure-registry env var,
// which would ship a production TLS downgrade switch to solve a test problem.
type registryClient interface {
	PullManifest(ctx context.Context, repo, digest string) ([]byte, error)
	PullBlob(ctx context.Context, repo, digest string, size, maxBytes int64, displayName, destDir string) (string, error)
}

// newClient builds the registry client for a host. Overridden in tests.
var newClient = func(host string) registryClient { return ocidigest.NewResolverForHost(host) }

// FetchParserModule materializes the parser module pinned by ref+digest into a
// temp file under destDir, verified against wantSHA256 at every step. It
// returns the temp file's path; the caller owns it (and removes it), which is
// the same contract as binmanager.DownloadAndVerifySHA256 — so the parser store
// publishes an OCI-sourced module through exactly the same atomic rename.
//
// The order matters: the manifest is fetched and checked against the artifact
// contract FIRST, so a manifest whose layer is not the declared module costs
// one small request and no payload bandwidth at all.
func FetchParserModule(ctx context.Context, ref, digest, wantSHA256, destDir, displayName string) (string, error) {
	// Explicit and early. authedGet guards too, but reaching it means the
	// refusal arrives inside a retry loop, and an offline pull has no business
	// spending a backoff schedule on a condition that cannot change.
	if err := httpx.GuardOffline("oci parser module pull of " + ref); err != nil {
		return "", err
	}
	if err := checkSHA256Hex(wantSHA256); err != nil {
		return "", err
	}

	host, repo, err := ociref.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("oci parser ref %q %w", ref, err)
	}
	if err := checkManifestDigest(digest); err != nil {
		return "", err
	}

	c := newClient(host)
	raw, err := c.PullManifest(ctx, repo, digest)
	if err != nil {
		// The bundle seeder owns ocidigest's not-found hint text ("was the
		// bundle garbage-collected?"), which is the wrong advice here.
		if errors.Is(err, ocidigest.ErrManifestNotFound) {
			return "", fmt.Errorf("%w: %s@%s (the artifact was removed or its tag was pruned; re-publish it or update the parser's oci.digest)",
				ErrModuleNotFound, ref, digest)
		}
		return "", fmt.Errorf("pull parser artifact manifest %s@%s: %w", ref, digest, err)
	}

	desc, err := SelectWasmLayer(raw, wantSHA256)
	if err != nil {
		return "", fmt.Errorf("parser artifact %s@%s: %w", ref, digest, err)
	}

	path, err := c.PullBlob(ctx, repo, desc.Digest.String(), desc.Size, MaxParserModuleBytes, displayName, destDir)
	if err != nil {
		return "", fmt.Errorf("pull parser module blob %s: %w", desc.Digest.String(), err)
	}
	return path, nil
}

// SelectWasmLayer validates an already digest-verified artifact manifest
// against the parser artifact contract and returns its single wasm layer
// descriptor. It is pure — no I/O, no network — so the whole policy is
// table-testable, and it runs before any blob request.
//
// Deliberately NOT a rule: the config blob's media type. The publisher emits
// the empty-JSON descriptor, and CI asserts that at publish time, but turning a
// producer-tooling detail into a consumer integrity rule would couple every
// already-published pin to whatever the publisher happens to emit — a future
// producer that wrote a typed config blob would break every pinned config with
// a fatal, non-degradable error. The trust anchor is the layer digest.
func SelectWasmLayer(manifestBytes []byte, wantSHA256 string) (ocispec.Descriptor, error) {
	var none ocispec.Descriptor
	if err := checkSHA256Hex(wantSHA256); err != nil {
		return none, err
	}

	// Probe for an index first, exactly as the bundle seeder does. A wasm module
	// is platform-independent, so an index here is either a mistake or a
	// substitution attempt; accepting one would also drag in the bundle's
	// platform-selection contract, which has no meaning for this artifact.
	var probe struct {
		MediaType string               `json:"mediaType"`
		Manifests []ocispec.Descriptor `json:"manifests"`
	}
	if err := json.Unmarshal(manifestBytes, &probe); err != nil {
		return none, fmt.Errorf("%w: malformed manifest: %w", errIntegrity, err)
	}
	if len(probe.Manifests) > 0 || isIndexMediaType(probe.MediaType) {
		return none, fmt.Errorf("%w: expected a single %s artifact manifest, got an index (mediaType %q, %d entries)",
			errIntegrity, ArtifactTypeParsers, probe.MediaType, len(probe.Manifests))
	}

	var m ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return none, fmt.Errorf("%w: malformed manifest: %w", errIntegrity, err)
	}

	if m.MediaType != ocispec.MediaTypeImageManifest {
		return none, fmt.Errorf("%w: manifest mediaType is %q, want %q", errIntegrity, m.MediaType, ocispec.MediaTypeImageManifest)
	}
	if m.ArtifactType != ArtifactTypeParsers {
		return none, fmt.Errorf("%w: artifactType is %q, want %q", errIntegrity, m.ArtifactType, ArtifactTypeParsers)
	}
	// A manifest with a subject is a referrer — a signature or an SBOM attached
	// to something else — not the module itself.
	if m.Subject != nil {
		return none, fmt.Errorf("%w: manifest has a subject (%s), so it is a referrer rather than the module",
			errIntegrity, m.Subject.Digest.String())
	}
	if len(m.Layers) != 1 {
		return none, fmt.Errorf("%w: expected exactly 1 layer, got %d", errIntegrity, len(m.Layers))
	}

	layer := m.Layers[0]
	if layer.MediaType != MediaTypeWasm {
		return none, fmt.Errorf("%w: layer mediaType is %q, want %q (the layer must be the uncompressed module)",
			errIntegrity, layer.MediaType, MediaTypeWasm)
	}
	// The pivot. The config's mandatory SHA-256 and the OCI layer digest are the
	// same number by construction, so this single comparison rejects a
	// substituted payload before it is requested.
	if want := sha256Prefix + wantSHA256; layer.Digest.String() != want {
		return none, fmt.Errorf("%w: layer digest is %s, want %s (the module the config pins by hash)",
			errIntegrity, layer.Digest.String(), want)
	}
	if layer.Size <= 0 || layer.Size > MaxParserModuleBytes {
		return none, fmt.Errorf("%w: layer size %d is outside (0, %d]", errIntegrity, layer.Size, MaxParserModuleBytes)
	}
	return layer, nil
}

// isIndexMediaType reports whether a media type denotes a multi-manifest index
// (OCI or the Docker manifest list it descends from).
func isIndexMediaType(mediaType string) bool {
	return mediaType == ocispec.MediaTypeImageIndex ||
		mediaType == "application/vnd.docker.distribution.manifest.list.v2+json"
}

// checkSHA256Hex enforces the bare 64-lowercase-hex form the config validator
// already guarantees. Re-checking here is not redundant: this package takes
// plain strings from any caller, and an empty hash silently comparing against
// "sha256:" is precisely the hash-less fetch the security policy forbids.
func checkSHA256Hex(hash string) error {
	if hash == "" {
		return fmt.Errorf("%w: no expected sha256 for the parser module (a hash is mandatory)", errIntegrity)
	}
	if !isLowerHex(hash, 64) {
		return fmt.Errorf("%w: expected sha256 %q must be 64 lowercase hex characters", errIntegrity, hash)
	}
	return nil
}

// checkManifestDigest enforces the "sha256:<64 hex>" pin form before a request
// is made, so a malformed digest is a local error instead of a registry 404.
func checkManifestDigest(digest string) error {
	if len(digest) != len(sha256Prefix)+64 || digest[:len(sha256Prefix)] != sha256Prefix ||
		!isLowerHex(digest[len(sha256Prefix):], 64) {
		return fmt.Errorf("%w: oci digest %q must be %q followed by 64 lowercase hex characters",
			errIntegrity, digest, sha256Prefix)
	}
	return nil
}

// isLowerHex reports whether s is exactly n lowercase hex characters.
func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
