package ocibundle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/target"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// seedManifestCache writes verified manifest bytes into the on-disk cache the
// registry source consults first, so status tests never touch the network.
func seedManifestCache(t *testing.T, digest string, body []byte) {
	t.Helper()
	cachePath := manifestCachePath(digest)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBundleStatus_NoBundleDeclared(t *testing.T) {
	testStore(t)
	if _, err := BundleStatus(context.Background(), &config.Config{}); err == nil {
		t.Fatal("expected error without an oci declaration")
	}
	if _, err := BundleStatus(context.Background(), nil); err == nil {
		t.Fatal("expected error for a nil config")
	}
}

func TestBundleStatus_ManifestWithCoverage(t *testing.T) {
	storeRoot := testStore(t)
	payload := []byte("status payload")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{
		"covered-tool":   payload,
		"uncovered-tool": []byte("not in bundle"),
	})

	manifest := ocispec.Manifest{
		MediaType:   ocispec.MediaTypeImageManifest,
		Annotations: map[string]string{AnnotationStoreRoot: testBuilderRoot},
		Layers: []ocispec.Descriptor{
			{
				MediaType: mediaTypeOCILayerGzip,
				Digest:    godigest.Digest(sha256DigestOf(payload)),
				Size:      int64(len(payload)),
				Annotations: map[string]string{
					AnnotationSubtree: subtrees["covered-tool"],
					AnnotationKind:    "binary",
					AnnotationApp:     "covered-tool",
				},
			},
			{
				// base layer without annotations: not store content
				MediaType: mediaTypeOCILayerGzip,
				Digest:    godigest.Digest(sha256DigestOf([]byte("base"))),
				Size:      4,
			},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256DigestOf(raw)
	seedManifestCache(t, digest, raw)
	cfg.OCI = &config.OCIRef{Ref: "registry.invalid/owner/repo", Digest: digest}

	// Materialize the covered subtree so Present flips for it.
	placed := filepath.Join(storeRoot, filepath.FromSlash(subtrees["covered-tool"]))
	if err := os.MkdirAll(filepath.Dir(placed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(placed, payload, 0o755); err != nil {
		t.Fatal(err)
	}

	status, err := BundleStatus(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BundleStatus: %v", err)
	}

	if status.Ref != cfg.OCI.Ref || status.Digest != digest {
		t.Errorf("status identity = %s@%s, want %s@%s", status.Ref, status.Digest, cfg.OCI.Ref, digest)
	}
	if status.Signed {
		t.Error("Signed = true without a signer")
	}
	if status.StoreRoot != testBuilderRoot {
		t.Errorf("StoreRoot = %q, want %q", status.StoreRoot, testBuilderRoot)
	}
	if len(status.Layers) != 1 {
		t.Fatalf("Layers = %d, want 1 (base layer skipped)", len(status.Layers))
	}
	layer := status.Layers[0]
	if layer.Subtree != subtrees["covered-tool"] || layer.Kind != "binary" || layer.App != "covered-tool" || !layer.Present {
		t.Errorf("unexpected layer status: %+v", layer)
	}

	coverage := map[string]AppCoverage{}
	for _, app := range status.Apps {
		coverage[app.App] = app
	}
	if !coverage["covered-tool"].Covered || !coverage["covered-tool"].Present {
		t.Errorf("covered-tool coverage = %+v, want covered and present", coverage["covered-tool"])
	}
	if coverage["uncovered-tool"].Covered || coverage["uncovered-tool"].Present {
		t.Errorf("uncovered-tool coverage = %+v, want neither covered nor present", coverage["uncovered-tool"])
	}
}

func TestBundleStatus_IndexSelectsHostPlatform(t *testing.T) {
	storeRoot := testStore(t)
	host := target.HostTarget()
	if host.OS == "linux" && host.Libc == target.LibcUnknown {
		t.Skip("host libc unknown; selection would intentionally refuse")
	}
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": []byte("x")})

	child := ocispec.Manifest{
		MediaType:   ocispec.MediaTypeImageManifest,
		Annotations: map[string]string{AnnotationStoreRoot: testBuilderRoot},
		Layers: []ocispec.Descriptor{{
			MediaType:   mediaTypeOCILayerGzip,
			Digest:      godigest.Digest(sha256DigestOf([]byte("x"))),
			Size:        1,
			Annotations: map[string]string{AnnotationSubtree: subtrees["tool"]},
		}},
	}
	childRaw, err := json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	childDigest := sha256DigestOf(childRaw)
	seedManifestCache(t, childDigest, childRaw)

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    godigest.Digest(childDigest),
		Platform:  &ocispec.Platform{OS: host.OS, Architecture: host.Arch},
	}
	if host.OS == "linux" {
		childDesc.Annotations = map[string]string{AnnotationLibc: string(host.Libc)}
	}
	idx := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			childDesc,
			{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    godigest.Digest(sha256DigestOf([]byte("attestation"))),
				Platform:  &ocispec.Platform{OS: "unknown", Architecture: "unknown"},
			},
		},
	}
	idxRaw, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	idxDigest := sha256DigestOf(idxRaw)
	seedManifestCache(t, idxDigest, idxRaw)

	// Pre-write the full-pull marker to cover the Seeded flag too. The subtree
	// it records has to be in the store: a marker whose content is gone reports
	// not-seeded, which is the whole point of checking it.
	placed := filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool"]))
	if err := os.MkdirAll(filepath.Dir(placed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(placed, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(storeRoot, "registry.invalid/owner/repo", idxDigest, []string{subtrees["tool"]}); err != nil {
		t.Fatal(err)
	}

	cfg.OCI = &config.OCIRef{Ref: "registry.invalid/owner/repo", Digest: idxDigest}
	status, err := BundleStatus(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BundleStatus: %v", err)
	}

	if len(status.Platforms) != 1 {
		t.Errorf("Platforms = %v, want exactly the non-attestation entry", status.Platforms)
	}
	if status.Selected == "" || !strings.HasPrefix(status.Selected, host.OS+"/") {
		t.Errorf("Selected = %q, want the %s entry", status.Selected, host.OS)
	}
	if !status.Seeded {
		t.Error("Seeded = false despite the marker")
	}
	if len(status.Layers) != 1 {
		t.Errorf("Layers = %d, want 1 from the child manifest", len(status.Layers))
	}
}

func TestBundleStatus_NoPlatformMatchReportsWithoutSelection(t *testing.T) {
	testStore(t)
	cfg, _ := testBinaryConfig(t, map[string][]byte{"tool": []byte("x")})

	idx := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    godigest.Digest(sha256DigestOf([]byte("other"))),
			Platform:  &ocispec.Platform{OS: "plan9", Architecture: "mips"},
		}},
	}
	idxRaw, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	idxDigest := sha256DigestOf(idxRaw)
	seedManifestCache(t, idxDigest, idxRaw)
	cfg.OCI = &config.OCIRef{
		Ref:    "registry.invalid/owner/repo",
		Digest: idxDigest,
		Signer: &config.OCISigner{Identity: "id", Issuer: "iss"},
	}

	status, err := BundleStatus(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BundleStatus: %v", err)
	}
	if status.Selected != "" {
		t.Errorf("Selected = %q, want empty for a platform miss", status.Selected)
	}
	if len(status.Platforms) != 1 || status.Platforms[0] != "plan9/mips" {
		t.Errorf("Platforms = %v, want [plan9/mips]", status.Platforms)
	}
	if !status.Signed {
		t.Error("Signed = false despite a pinned signer")
	}
	if len(status.Layers) != 0 {
		t.Errorf("Layers = %d, want none without a selected manifest", len(status.Layers))
	}
}
