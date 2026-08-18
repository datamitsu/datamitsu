package ociartifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/ocidigest"
)

// moduleSHA256 is the layer digest recorded in the golden manifest — i.e. the
// value a config would carry as the parser's mandatory `hash`.
const moduleSHA256 = "612a5c2da01d74a35fc0a27ac01ac9ae92442cbdc8bc6ddddee4a32642a9d73f"

// goldenManifest returns the published artifact manifest exactly as the release
// job writes it. Keeping the wire format in testdata (rather than building it
// from ocispec structs) means the consumer policy is tested against the bytes a
// producer actually emits, including the fields Go would otherwise fill in.
func goldenManifest(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "parsers-artifact-manifest.json"))
	if err != nil {
		t.Fatalf("read golden manifest: %v", err)
	}
	return raw
}

// mutate parses the golden manifest, applies fn to the decoded object and
// re-encodes it, so each rejection case reads as "the published artifact, but
// with X wrong".
func mutate(t *testing.T, fn func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(goldenManifest(t), &m); err != nil {
		t.Fatalf("decode golden manifest: %v", err)
	}
	fn(m)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode mutated manifest: %v", err)
	}
	return raw
}

// layer0 returns the golden manifest's single layer object for mutation.
func layer0(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	layers, ok := m["layers"].([]any)
	if !ok || len(layers) != 1 {
		t.Fatalf("golden manifest does not have exactly one layer: %v", m["layers"])
	}
	layer, ok := layers[0].(map[string]any)
	if !ok {
		t.Fatalf("golden manifest layer is not an object: %v", layers[0])
	}
	return layer
}

func TestSelectWasmLayer_ValidArtifact(t *testing.T) {
	desc, err := SelectWasmLayer(goldenManifest(t), moduleSHA256)
	if err != nil {
		t.Fatalf("SelectWasmLayer() error = %v", err)
	}
	if got := desc.Digest.String(); got != "sha256:"+moduleSHA256 {
		t.Errorf("descriptor digest = %q, want the module hash", got)
	}
	if desc.MediaType != MediaTypeWasm {
		t.Errorf("descriptor mediaType = %q, want %q", desc.MediaType, MediaTypeWasm)
	}
	if desc.Size != 385667 {
		t.Errorf("descriptor size = %d, want the size the publisher recorded", desc.Size)
	}
}

// TestSelectWasmLayer_AcceptsAnyConfigMediaType pins a deliberate NON-rule. The
// empty-JSON config blob is a publishing invariant asserted in CI, not a
// consumer gate: making it one would couple every already-published pin to
// whatever the publisher emits, so a future producer that wrote a typed config
// blob would break every pinned config with a fatal, non-degradable error. The
// consumer never fetches the config blob at all.
func TestSelectWasmLayer_AcceptsAnyConfigMediaType(t *testing.T) {
	raw := mutate(t, func(m map[string]any) {
		m["config"] = map[string]any{
			"mediaType": "application/vnd.datamitsu.parsers.config.v1+json",
			"digest":    "sha256:" + strings.Repeat("ab", 32),
			"size":      float64(123),
		}
	})
	if _, err := SelectWasmLayer(raw, moduleSHA256); err != nil {
		t.Errorf("SelectWasmLayer() rejected a differently-typed config blob: %v", err)
	}
}

func TestSelectWasmLayer_Rejects(t *testing.T) {
	cases := map[string]struct {
		manifest []byte
		wantMsg  string
	}{
		"oci index": {
			manifest: mutate(t, func(m map[string]any) {
				m["mediaType"] = "application/vnd.oci.image.index.v1+json"
				m["manifests"] = []any{map[string]any{"digest": "sha256:" + strings.Repeat("ab", 32)}}
				delete(m, "layers")
			}),
			wantMsg: "got an index",
		},
		"docker manifest list": {
			manifest: mutate(t, func(m map[string]any) {
				m["mediaType"] = "application/vnd.docker.distribution.manifest.list.v2+json"
				delete(m, "layers")
			}),
			wantMsg: "got an index",
		},
		"image manifest carrying a manifests array": {
			manifest: mutate(t, func(m map[string]any) {
				m["manifests"] = []any{map[string]any{"digest": "sha256:" + strings.Repeat("cd", 32)}}
			}),
			wantMsg: "got an index",
		},
		"wrong manifest media type": {
			manifest: mutate(t, func(m map[string]any) {
				m["mediaType"] = "application/vnd.docker.distribution.manifest.v2+json"
			}),
			wantMsg: "manifest mediaType",
		},
		"missing artifactType": {
			manifest: mutate(t, func(m map[string]any) { delete(m, "artifactType") }),
			wantMsg:  "artifactType",
		},
		"wrong artifactType": {
			manifest: mutate(t, func(m map[string]any) {
				m["artifactType"] = "application/vnd.datamitsu.bundle.v1+tar"
			}),
			wantMsg: "artifactType",
		},
		"referrer with a subject": {
			manifest: mutate(t, func(m map[string]any) {
				m["subject"] = map[string]any{
					"mediaType": "application/vnd.oci.image.manifest.v1+json",
					"digest":    "sha256:" + strings.Repeat("ef", 32),
					"size":      float64(500),
				}
			}),
			wantMsg: "referrer",
		},
		"no layers": {
			manifest: mutate(t, func(m map[string]any) { m["layers"] = []any{} }),
			wantMsg:  "exactly 1 layer",
		},
		"two layers": {
			manifest: mutate(t, func(m map[string]any) {
				layers, _ := m["layers"].([]any)
				m["layers"] = append(layers, layer0(t, m))
			}),
			wantMsg: "exactly 1 layer",
		},
		"compressed layer": {
			manifest: mutate(t, func(m map[string]any) {
				layer0(t, m)["mediaType"] = "application/vnd.oci.image.layer.v1.tar+gzip"
			}),
			wantMsg: "layer mediaType",
		},
		"layer digest is not the declared module": {
			manifest: mutate(t, func(m map[string]any) {
				layer0(t, m)["digest"] = "sha256:" + strings.Repeat("99", 32)
			}),
			wantMsg: "layer digest",
		},
		"empty layer": {
			manifest: mutate(t, func(m map[string]any) { layer0(t, m)["size"] = float64(0) }),
			wantMsg:  "layer size",
		},
		"layer over the size cap": {
			manifest: mutate(t, func(m map[string]any) {
				layer0(t, m)["size"] = float64(MaxParserModuleBytes + 1)
			}),
			wantMsg: "layer size",
		},
		"malformed json": {
			manifest: []byte("{not json"),
			wantMsg:  "malformed manifest",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := SelectWasmLayer(tc.manifest, moduleSHA256)
			if err == nil {
				t.Fatal("SelectWasmLayer() accepted a manifest that violates the artifact contract")
			}
			if !IsIntegrityError(err) {
				t.Errorf("error is not an integrity error: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not mention %q", err, tc.wantMsg)
			}
		})
	}
}

// TestSelectWasmLayer_DigestMismatchNamesBoth keeps the failure actionable: the
// operator has to be able to see which module the registry offered and which
// one the config pins, without turning on debug logging.
func TestSelectWasmLayer_DigestMismatchNamesBoth(t *testing.T) {
	other := strings.Repeat("99", 32)
	raw := mutate(t, func(m map[string]any) { layer0(t, m)["digest"] = "sha256:" + other })

	_, err := SelectWasmLayer(raw, moduleSHA256)
	if err == nil {
		t.Fatal("expected a rejection")
	}
	for _, want := range []string{"sha256:" + other, "sha256:" + moduleSHA256} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestSelectWasmLayer_HashIsMandatory(t *testing.T) {
	for name, hash := range map[string]string{
		"empty":     "",
		"uppercase": strings.ToUpper(moduleSHA256),
		"short":     moduleSHA256[:32],
		"prefixed":  "sha256:" + moduleSHA256,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SelectWasmLayer(goldenManifest(t), hash)
			if err == nil {
				t.Fatalf("SelectWasmLayer() accepted %q as the expected hash", hash)
			}
			if !IsIntegrityError(err) {
				t.Errorf("error is not an integrity error: %v", err)
			}
		})
	}
}

// fakeRegistry records what a fetch actually asked the registry for. The blob
// counter is the interesting one: the enterprise claim is that a substituted
// payload is rejected before it is requested.
type fakeRegistry struct {
	manifest      []byte
	manifestErr   error
	manifestCalls int
	blobCalls     int
	blobBytes     []byte
	lastBlobDgst  string
}

func (f *fakeRegistry) PullManifest(_ context.Context, _, _ string) ([]byte, error) {
	f.manifestCalls++
	if f.manifestErr != nil {
		return nil, f.manifestErr
	}
	return f.manifest, nil
}

func (f *fakeRegistry) PullBlob(_ context.Context, _, digest string, _, _ int64, _, destDir string) (string, error) {
	f.blobCalls++
	f.lastBlobDgst = digest
	path := filepath.Join(destDir, "oci-blob-fake")
	if err := os.WriteFile(path, f.blobBytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// withFakeRegistry installs a fake client for the duration of a test and
// returns it, along with a counter of how many times a client was constructed —
// which is how "a malformed ref never reaches the network" is asserted.
func withFakeRegistry(t *testing.T, f *fakeRegistry) *int {
	t.Helper()
	constructed := 0
	orig := newClient
	newClient = func(string) registryClient {
		constructed++
		return f
	}
	t.Cleanup(func() { newClient = orig })
	return &constructed
}

const testRef = "ghcr.io/datamitsu/datamitsu-parsers"

var testDigest = "sha256:" + strings.Repeat("ab", 32)

func TestFetchParserModule_HappyPath(t *testing.T) {
	fake := &fakeRegistry{manifest: goldenManifest(t), blobBytes: []byte("\x00asm-module")}
	withFakeRegistry(t, fake)

	dir := t.TempDir()
	path, err := FetchParserModule(context.Background(), testRef, testDigest, moduleSHA256, dir, "core")
	if err != nil {
		t.Fatalf("FetchParserModule() error = %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("module materialized at %q, want a temp file under %q", path, dir)
	}
	if fake.blobCalls != 1 {
		t.Errorf("blob calls = %d, want 1", fake.blobCalls)
	}
	if fake.lastBlobDgst != "sha256:"+moduleSHA256 {
		t.Errorf("blob pulled by digest %q, want the layer digest", fake.lastBlobDgst)
	}
}

// TestFetchParserModule_MismatchIssuesNoBlobRequest is the pre-flight-rejection
// property the whole design is built on: the manifest pivot runs before any
// payload byte is requested, so a registry serving a correctly-digested
// manifest pointing at other content costs one small request and nothing else.
func TestFetchParserModule_MismatchIssuesNoBlobRequest(t *testing.T) {
	raw := mutate(t, func(m map[string]any) {
		layer0(t, m)["digest"] = "sha256:" + strings.Repeat("99", 32)
	})
	fake := &fakeRegistry{manifest: raw}
	withFakeRegistry(t, fake)

	_, err := FetchParserModule(context.Background(), testRef, testDigest, moduleSHA256, t.TempDir(), "core")
	if err == nil {
		t.Fatal("FetchParserModule() accepted a substituted layer")
	}
	if !IsIntegrityError(err) {
		t.Errorf("error is not an integrity error: %v", err)
	}
	if fake.manifestCalls != 1 {
		t.Errorf("manifest calls = %d, want 1", fake.manifestCalls)
	}
	if fake.blobCalls != 0 {
		t.Errorf("blob calls = %d, want 0 — the payload must never be requested", fake.blobCalls)
	}
}

func TestFetchParserModule_MalformedRefNeverBuildsAClient(t *testing.T) {
	for name, ref := range map[string]string{
		"no host":       "datamitsu/parsers",
		"tag in ref":    "ghcr.io/datamitsu/parsers:latest",
		"digest in ref": "ghcr.io/datamitsu/parsers@" + testDigest,
		"uppercase":     "ghcr.io/Datamitsu/Parsers",
		"empty":         "",
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeRegistry{manifest: goldenManifest(t)}
			constructed := withFakeRegistry(t, fake)

			_, err := FetchParserModule(context.Background(), ref, testDigest, moduleSHA256, t.TempDir(), "core")
			if err == nil {
				t.Fatalf("FetchParserModule() accepted ref %q", ref)
			}
			if *constructed != 0 {
				t.Errorf("a registry client was constructed for an invalid ref %q", ref)
			}
		})
	}
}

func TestFetchParserModule_MalformedDigestNeverReachesTheRegistry(t *testing.T) {
	for name, digest := range map[string]string{
		"empty":           "",
		"no prefix":       strings.Repeat("ab", 32),
		"wrong algorithm": "sha512:" + strings.Repeat("ab", 32),
		"uppercase":       "sha256:" + strings.ToUpper(strings.Repeat("ab", 32)),
		"tag":             "latest",
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeRegistry{manifest: goldenManifest(t)}
			withFakeRegistry(t, fake)

			_, err := FetchParserModule(context.Background(), testRef, digest, moduleSHA256, t.TempDir(), "core")
			if err == nil {
				t.Fatalf("FetchParserModule() accepted digest %q", digest)
			}
			if fake.manifestCalls != 0 {
				t.Errorf("manifest calls = %d, want 0 for a malformed pin", fake.manifestCalls)
			}
		})
	}
}

// TestFetchParserModule_OfflineIsRefusedImmediately pins that DATAMITSU_OFFLINE
// is the hard network gate for this path too — and that the refusal costs no
// requests and no retry backoff.
func TestFetchParserModule_OfflineIsRefusedImmediately(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	fake := &fakeRegistry{manifest: goldenManifest(t)}
	constructed := withFakeRegistry(t, fake)

	_, err := FetchParserModule(context.Background(), testRef, testDigest, moduleSHA256, t.TempDir(), "core")
	if err == nil {
		t.Fatal("FetchParserModule() ran a registry pull in offline mode")
	}
	if !errors.Is(err, httpx.ErrOffline) {
		t.Errorf("error = %v, want it to wrap httpx.ErrOffline", err)
	}
	if *constructed != 0 || fake.manifestCalls != 0 {
		t.Errorf("offline mode still touched the registry (clients=%d, manifests=%d)", *constructed, fake.manifestCalls)
	}
}

// TestFetchParserModule_NotFoundIsRewordedForParsers keeps the bundle seeder's
// advice ("was the bundle garbage-collected?") out of an error about a parser,
// where it would send the reader to the wrong config key.
func TestFetchParserModule_NotFoundIsRewordedForParsers(t *testing.T) {
	fake := &fakeRegistry{manifestErr: ocidigest.ErrManifestNotFound}
	withFakeRegistry(t, fake)

	_, err := FetchParserModule(context.Background(), testRef, testDigest, moduleSHA256, t.TempDir(), "core")
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !errors.Is(err, ErrModuleNotFound) {
		t.Errorf("error = %v, want it to wrap ErrModuleNotFound", err)
	}
	if strings.Contains(err.Error(), "bundle") {
		t.Errorf("error %q still carries the bundle-specific hint", err)
	}
	if !strings.Contains(err.Error(), "oci.digest") {
		t.Errorf("error %q does not name the config key to update", err)
	}
}
