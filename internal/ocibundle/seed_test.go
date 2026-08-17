package ocibundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"

	"github.com/klauspost/compress/zstd"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const testBuilderRoot = "/dm/store"

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sha256DigestOf(data []byte) string { return "sha256:" + sha256Hex(data) }

// tarEntry describes one entry of a synthetic layer tar.
type tarEntry struct {
	name     string
	typeflag byte
	content  []byte
	linkname string
	mode     int64
}

func dirEntry(name string) tarEntry { return tarEntry{name: name, typeflag: tar.TypeDir, mode: 0o755} }

func fileEntry(name string, content []byte) tarEntry {
	return tarEntry{name: name, typeflag: tar.TypeReg, content: content, mode: 0o755}
}

// buildLayer produces a gzip-compressed tar layer from entries.
func buildLayer(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     e.mode,
			Linkname: e.linkname,
			Size:     int64(len(e.content)),
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", e.name, err)
		}
		if len(e.content) > 0 {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatalf("write tar content %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// subtreeLayer builds a layer carrying content under testBuilderRoot/subtree.
// parents creates the structural parent dirs every real layer tar has.
func subtreeLayer(t *testing.T, subtree string, entries []tarEntry) []byte {
	t.Helper()
	prefix := strings.TrimPrefix(testBuilderRoot, "/")
	var all []tarEntry
	segments := strings.Split(prefix+"/"+subtree, "/")
	for i := 1; i < len(segments); i++ {
		all = append(all, dirEntry(strings.Join(segments[:i], "/")+"/"))
	}
	all = append(all, entries...)
	return buildLayer(t, all)
}

// fakeSource is an in-memory blobSource with request counters.
type fakeSource struct {
	manifests     map[string][]byte
	blobs         map[string][]byte
	blobErrors    map[string]error
	manifestCalls atomic.Int64
	blobCalls     atomic.Int64
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		manifests:  make(map[string][]byte),
		blobs:      make(map[string][]byte),
		blobErrors: make(map[string]error),
	}
}

func (s *fakeSource) manifest(_ context.Context, digest string) ([]byte, error) {
	s.manifestCalls.Add(1)
	data, ok := s.manifests[digest]
	if !ok {
		return nil, fmt.Errorf("manifest %s not in fake source", digest)
	}
	return data, nil
}

func (s *fakeSource) blob(_ context.Context, desc ocispec.Descriptor, destDir, _ string) (string, error) {
	s.blobCalls.Add(1)
	if err := s.blobErrors[desc.Digest.String()]; err != nil {
		return "", err
	}
	data, ok := s.blobs[desc.Digest.String()]
	if !ok {
		return "", fmt.Errorf("blob %s not in fake source", desc.Digest)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(destDir, "fake-blob-*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

// addLayer registers a blob and returns its annotated descriptor.
func (s *fakeSource) addLayer(blob []byte, subtree string) ocispec.Descriptor {
	dgst := sha256DigestOf(blob)
	s.blobs[dgst] = blob
	desc := ocispec.Descriptor{
		MediaType: mediaTypeOCILayerGzip,
		Digest:    godigest.Digest(dgst),
		Size:      int64(len(blob)),
	}
	if subtree != "" {
		desc.Annotations = map[string]string{AnnotationSubtree: subtree}
	}
	return desc
}

// addManifest registers a manifest with the standard store-root annotation
// and returns its digest.
func (s *fakeSource) addManifest(t *testing.T, layers []ocispec.Descriptor, annotations map[string]string) string {
	t.Helper()
	if annotations == nil {
		annotations = map[string]string{AnnotationStoreRoot: testBuilderRoot}
	}
	manifest := ocispec.Manifest{
		MediaType:   ocispec.MediaTypeImageManifest,
		Layers:      layers,
		Annotations: annotations,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	dgst := sha256DigestOf(data)
	s.manifests[dgst] = data
	return dgst
}

// testStore points the store at a fresh temp dir and returns its root.
func testStore(t *testing.T) string {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	return env.GetStorePath()
}

// testBinaryConfig builds a config with single-file binary apps for the host
// target and returns the config plus each app's expected store subtree.
func testBinaryConfig(t *testing.T, payloads map[string][]byte) (*config.Config, map[string]string) {
	t.Helper()
	host := target.HostTarget()
	apps := make(binmanager.MapOfApps)
	for name, payload := range payloads {
		apps[name] = binmanager.App{
			Required: true,
			Binary: &binmanager.AppConfigBinary{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsType(host.OS): {
						syslist.ArchType(host.Arch): {
							string(host.Libc): binmanager.BinaryOsArchInfo{
								URL:         "https://example.invalid/" + name,
								Hash:        sha256Hex(payload),
								ContentType: binmanager.BinContentTypeBinary,
							},
						},
					},
				},
			},
		}
	}
	cfg := &config.Config{Apps: apps}

	storeRoot := env.GetStorePath()
	bm := binmanager.New(cfg.Apps, nil, nil)
	subtrees := make(map[string]string, len(payloads))
	for name := range payloads {
		abs, err := bm.ComputeInstallPath(name)
		if err != nil {
			t.Fatalf("ComputeInstallPath(%s): %v", name, err)
		}
		rel, err := subtreeRel(storeRoot, abs)
		if err != nil {
			t.Fatalf("subtreeRel(%s): %v", name, err)
		}
		subtrees[name] = rel
	}
	return cfg, subtrees
}

// binaryLayer builds the layer for a single-file binary subtree.
func binaryLayer(t *testing.T, subtree string, payload []byte) []byte {
	t.Helper()
	prefix := strings.TrimPrefix(testBuilderRoot, "/")
	return subtreeLayer(t, subtree, []tarEntry{fileEntry(prefix+"/"+subtree, payload)})
}

func TestSeed_FullPullPlacesBinaryAndWritesMarker(t *testing.T) {
	storeRoot := testStore(t)
	payload := []byte("binary payload v1")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": payload})

	src := newFakeSource()
	base := buildLayer(t, []tarEntry{dirEntry("etc/"), fileEntry("etc/os-release", []byte("ID=test"))})
	layers := []ocispec.Descriptor{
		src.addLayer(base, ""), // base rootfs layer without annotation: skipped
		src.addLayer(binaryLayer(t, subtrees["tool"], payload), subtrees["tool"]),
	}
	digest := src.addManifest(t, layers, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}

	placed := filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool"]))
	data, err := os.ReadFile(placed)
	if err != nil {
		t.Fatalf("seeded binary missing: %v", err)
	}
	if string(data) != string(payload) {
		t.Errorf("seeded content = %q, want %q", data, payload)
	}
	if !markerExists(storeRoot, digest) {
		t.Error("full pull did not write the seed marker")
	}

	// Repeat pull: the marker short-circuits before any source access.
	src.manifestCalls.Store(0)
	src.blobCalls.Store(0)
	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("repeat seedFrom: %v", err)
	}
	if src.manifestCalls.Load() != 0 || src.blobCalls.Load() != 0 {
		t.Errorf("repeat full pull touched the source (manifests=%d blobs=%d), want full no-op",
			src.manifestCalls.Load(), src.blobCalls.Load())
	}
}

func TestSeed_DemandDrivenPullsOnlyNeededLayers(t *testing.T) {
	storeRoot := testStore(t)
	payloads := map[string][]byte{
		"tool-one": []byte("payload one"),
		"tool-two": []byte("payload two"),
	}
	cfg, subtrees := testBinaryConfig(t, payloads)

	src := newFakeSource()
	layers := make([]ocispec.Descriptor, 0, len(payloads))
	for name, payload := range payloads {
		layers = append(layers, src.addLayer(binaryLayer(t, subtrees[name], payload), subtrees[name]))
	}
	digest := src.addManifest(t, layers, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{Needed: []string{"tool-one"}}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}

	if src.blobCalls.Load() != 1 {
		t.Errorf("blob calls = %d, want exactly 1 (only the needed layer)", src.blobCalls.Load())
	}
	if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool-one"]))); err != nil {
		t.Errorf("needed tool not seeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool-two"]))); err == nil {
		t.Error("unneeded tool was seeded")
	}
	if markerExists(storeRoot, digest) {
		t.Error("demand-driven pull must not write the full-pull marker")
	}

	// All needed subtrees present: the repeat run is a zero-network no-op.
	src.manifestCalls.Store(0)
	src.blobCalls.Store(0)
	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{Needed: []string{"tool-one"}}); err != nil {
		t.Fatalf("repeat seedFrom: %v", err)
	}
	if src.manifestCalls.Load() != 0 || src.blobCalls.Load() != 0 {
		t.Errorf("repeat demand-driven pull touched the source (manifests=%d blobs=%d)",
			src.manifestCalls.Load(), src.blobCalls.Load())
	}
}

func TestSeed_WarmSubtreeSkipsBlobDownload(t *testing.T) {
	storeRoot := testStore(t)
	payload := []byte("already here")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": payload})

	placed := filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool"]))
	if err := os.MkdirAll(filepath.Dir(placed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(placed, payload, 0o755); err != nil {
		t.Fatal(err)
	}

	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{
		src.addLayer(binaryLayer(t, subtrees["tool"], payload), subtrees["tool"]),
	}, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}
	if src.blobCalls.Load() != 0 {
		t.Errorf("blob calls = %d, want 0 for a warm subtree", src.blobCalls.Load())
	}
	if !markerExists(storeRoot, digest) {
		t.Error("full pull over a warm store should still write the marker")
	}
}

// The marker records what a full pull laid down, so a re-seed can check that
// it is still true instead of trusting that it once was. Bare existence made
// `store seed` a no-op exactly when someone re-runs it: after the store lost
// content, or after a core upgrade moved a subtree, leaving an airgapped run
// to fail later with nothing to fetch from.
func TestSeed_StaleMarkerRePullsInsteadOfSkipping(t *testing.T) {
	storeRoot := testStore(t)
	payload := []byte("binary payload v1")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": payload})

	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{
		src.addLayer(binaryLayer(t, subtrees["tool"], payload), subtrees["tool"]),
	}, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("first seedFrom: %v", err)
	}
	firstPull := src.blobCalls.Load()
	if firstPull == 0 {
		t.Fatal("the first full pull fetched nothing")
	}

	// A second run over an intact store is still the cheap no-op it was.
	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("second seedFrom: %v", err)
	}
	if src.blobCalls.Load() != firstPull {
		t.Errorf("blob calls = %d, want %d: an intact store must still short-circuit on the marker", src.blobCalls.Load(), firstPull)
	}

	// The content goes; the marker stays.
	placed := filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool"]))
	if err := os.RemoveAll(placed); err != nil {
		t.Fatal(err)
	}
	if !markerExists(storeRoot, digest) {
		t.Fatal("the marker should have survived removing the content")
	}

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("third seedFrom: %v", err)
	}
	if src.blobCalls.Load() <= firstPull {
		t.Errorf("blob calls = %d, want more than %d: a stale marker must not skip the pull", src.blobCalls.Load(), firstPull)
	}
	if _, err := os.Stat(placed); err != nil {
		t.Errorf("re-seed did not restore the subtree: %v", err)
	}
}

func TestSeed_ReVerificationCatchesSwappedBinary(t *testing.T) {
	storeRoot := testStore(t)
	published := []byte("the published binary")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": published})

	swapped := []byte("attacker-swapped binary")
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{
		src.addLayer(binaryLayer(t, subtrees["tool"], swapped), subtrees["tool"]),
	}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil {
		t.Fatal("expected re-verification failure, got nil")
	}
	if !IsFatalSeedError(err) {
		t.Errorf("IsFatalSeedError(%v) = false, want true", err)
	}
	if !strings.Contains(err.Error(), "published SHA-256") {
		t.Errorf("error %q should mention the published SHA-256", err)
	}
	if _, statErr := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool"]))); statErr == nil {
		t.Error("swapped content must not be placed into the store")
	}
	if markerExists(storeRoot, digest) {
		t.Error("failed pull must not write the marker")
	}
}

func TestSeed_MalformedSubtreeAnnotationIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}
	src := newFakeSource()

	for _, bad := range []string{"../evil", "/abs/path", ".bin/../../evil", "outside/root"} {
		blob := buildLayer(t, []tarEntry{fileEntry("dm/store/x", []byte("x"))})
		digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(blob, bad)}, nil)
		err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
		if err == nil {
			t.Fatalf("subtree %q: expected fatal error, got nil", bad)
		}
		if !IsFatalSeedError(err) {
			t.Errorf("subtree %q: IsFatalSeedError = false, want true (%v)", bad, err)
		}
	}
}

func TestSeed_EntryOutsideDeclaredSubtreeIsFatal(t *testing.T) {
	storeRoot := testStore(t)
	payload := []byte("payload")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": payload})

	// The layer declares tool's subtree but smuggles a file into another app's
	// subtree — the write-allowlist must reject the whole layer.
	prefix := strings.TrimPrefix(testBuilderRoot, "/")
	evil := subtreeLayer(t, subtrees["tool"], []tarEntry{
		fileEntry(prefix+"/"+subtrees["tool"], payload),
		fileEntry(prefix+"/.bin/other-tool/deadbeef", []byte("overwrite")),
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(evil, subtrees["tool"])}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil {
		t.Fatal("expected allowlist violation, got nil")
	}
	if !IsFatalSeedError(err) {
		t.Errorf("IsFatalSeedError(%v) = false, want true", err)
	}
	if _, statErr := os.Stat(filepath.Join(storeRoot, ".bin", "other-tool")); statErr == nil {
		t.Error("cross-subtree content must never reach the store")
	}
}

func TestSeed_RuntimeAppRelocation(t *testing.T) {
	storeRoot := testStore(t)
	cfg := &config.Config{}

	subtree := ".apps/uv/myapp/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	pyvenv := "home = " + testBuilderRoot + "/.uv/python/cpython-3.12\nversion = 3.12\n"
	script := "#!" + testBuilderRoot + "/" + subtree + "/.venv/bin/python\nprint('hi')\n"
	layer := subtreeLayer(t, subtree, []tarEntry{
		dirEntry(prefix + "/.venv/"),
		dirEntry(prefix + "/.venv/bin/"),
		fileEntry(prefix+"/.venv/pyvenv.cfg", []byte(pyvenv)),
		{
			name: prefix + "/.venv/bin/python", typeflag: tar.TypeSymlink,
			linkname: testBuilderRoot + "/.uv/python/cpython-3.12/bin/python3",
		},
		fileEntry(prefix+"/.venv/bin/mytool", []byte(script)),
		fileEntry(prefix+"/data/original.txt", []byte("shared content")),
		{name: prefix + "/data/link.txt", typeflag: tar.TypeLink, linkname: prefix + "/data/original.txt"},
		{name: prefix + "/.venv/relative", typeflag: tar.TypeSymlink, linkname: "bin/python"},
	})

	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)
	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}

	root := filepath.Join(storeRoot, filepath.FromSlash(subtree))

	linkTarget, err := os.Readlink(filepath.Join(root, ".venv", "bin", "python"))
	if err != nil {
		t.Fatalf("read venv symlink: %v", err)
	}
	wantTarget := filepath.Join(storeRoot, ".uv", "python", "cpython-3.12", "bin", "python3")
	if linkTarget != wantTarget {
		t.Errorf("venv symlink = %q, want relocated %q", linkTarget, wantTarget)
	}

	cfgData, err := os.ReadFile(filepath.Join(root, ".venv", "pyvenv.cfg"))
	if err != nil {
		t.Fatalf("read pyvenv.cfg: %v", err)
	}
	if strings.Contains(string(cfgData), testBuilderRoot) {
		t.Errorf("pyvenv.cfg still references the builder store root: %q", cfgData)
	}
	if !strings.Contains(string(cfgData), storeRoot) {
		t.Errorf("pyvenv.cfg not rewritten to the consumer store root: %q", cfgData)
	}

	scriptData, err := os.ReadFile(filepath.Join(root, ".venv", "bin", "mytool"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	firstLine, _, _ := strings.Cut(string(scriptData), "\n")
	if strings.Contains(firstLine, testBuilderRoot) {
		t.Errorf("shebang still references the builder store root: %q", firstLine)
	}
	if !strings.HasPrefix(firstLine, "#!"+storeRoot) {
		t.Errorf("shebang not rewritten: %q", firstLine)
	}
	if !strings.Contains(string(scriptData), "print('hi')") {
		t.Errorf("script body damaged: %q", scriptData)
	}

	origInfo, err := os.Stat(filepath.Join(root, "data", "original.txt"))
	if err != nil {
		t.Fatalf("stat original: %v", err)
	}
	linkInfo, err := os.Stat(filepath.Join(root, "data", "link.txt"))
	if err != nil {
		t.Fatalf("stat hardlink: %v", err)
	}
	if !os.SameFile(origInfo, linkInfo) {
		t.Error("hardlink was not restored as a hardlink")
	}

	relTarget, err := os.Readlink(filepath.Join(root, ".venv", "relative"))
	if err != nil {
		t.Fatalf("read relative symlink: %v", err)
	}
	if relTarget != "bin/python" {
		t.Errorf("relative symlink = %q, want kept verbatim", relTarget)
	}
}

func TestSeed_HardlinkOutsideSubtreeIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}

	subtree := ".apps/node/myapp/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	layer := subtreeLayer(t, subtree, []tarEntry{
		{
			name: prefix + "/stolen", typeflag: tar.TypeLink,
			linkname: strings.TrimPrefix(testBuilderRoot, "/") + "/.pnpm-store/v3/files/ab/cdef",
		},
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil {
		t.Fatal("expected hardlink-outside-subtree error, got nil")
	}
	if !strings.Contains(err.Error(), "hardlink") {
		t.Errorf("error %q should mention the hardlink", err)
	}
}

func TestSeed_PartialFailureWritesNoMarker(t *testing.T) {
	storeRoot := testStore(t)
	payloads := map[string][]byte{
		"tool-ok":  []byte("good payload"),
		"tool-bad": []byte("never arrives"),
	}
	cfg, subtrees := testBinaryConfig(t, payloads)

	src := newFakeSource()
	okDesc := src.addLayer(binaryLayer(t, subtrees["tool-ok"], payloads["tool-ok"]), subtrees["tool-ok"])
	badDesc := src.addLayer(binaryLayer(t, subtrees["tool-bad"], payloads["tool-bad"]), subtrees["tool-bad"])
	src.blobErrors[badDesc.Digest.String()] = errors.New("network exploded")
	digest := src.addManifest(t, []ocispec.Descriptor{okDesc, badDesc}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil {
		t.Fatal("expected partial failure, got nil")
	}
	if markerExists(storeRoot, digest) {
		t.Error("partial failure must not write the marker")
	}
}

func TestSeed_MissingStoreRootAnnotationIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}
	src := newFakeSource()
	blob := buildLayer(t, []tarEntry{fileEntry("dm/store/.bin/x/h", []byte("x"))})
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(blob, ".bin/x/h")},
		map[string]string{"some.other": "annotation"})

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil || !IsFatalSeedError(err) {
		t.Fatalf("expected fatal missing store-root error, got %v", err)
	}
}

func TestSeed_UnknownLayerMediaTypeIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}
	src := newFakeSource()
	blob := buildLayer(t, []tarEntry{fileEntry("dm/store/.bin/x/h", []byte("x"))})
	desc := src.addLayer(blob, ".bin/x/h")
	desc.MediaType = "application/vnd.oci.image.layer.v1.tar+xz"
	digest := src.addManifest(t, []ocispec.Descriptor{desc}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil || !IsFatalSeedError(err) {
		t.Fatalf("expected fatal media-type error, got %v", err)
	}
}

func TestSeed_SignerIsUnsupported(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}
	src := newFakeSource()
	digest := src.addManifest(t, nil, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest,
		&config.OCISigner{Identity: "id", Issuer: "iss"}, Options{})
	if err == nil || !IsFatalSeedError(err) {
		t.Fatalf("expected fatal signer-unsupported error, got %v", err)
	}
}

func TestSelectDescriptor_LibcDimension(t *testing.T) {
	manifestDesc := func(os, arch, libc string) ocispec.Descriptor {
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    godigest.Digest(sha256DigestOf([]byte(os + arch + libc))),
			Platform:  &ocispec.Platform{OS: os, Architecture: arch},
		}
		if libc != "" {
			desc.Annotations = map[string]string{AnnotationLibc: libc}
		}
		return desc
	}
	idx := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			manifestDesc("unknown", "unknown", ""), // buildx attestation
			manifestDesc("linux", "amd64", "glibc"),
			manifestDesc("linux", "amd64", "musl"),
			manifestDesc("darwin", "arm64", ""),
		},
	}

	t.Run("glibc host selects the glibc entry", func(t *testing.T) {
		desc, err := selectDescriptor(idx, target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc})
		if err != nil {
			t.Fatalf("selectDescriptor: %v", err)
		}
		if desc.Annotations[AnnotationLibc] != "glibc" {
			t.Errorf("selected libc = %q, want glibc", desc.Annotations[AnnotationLibc])
		}
	})

	t.Run("musl host selects the musl entry", func(t *testing.T) {
		desc, err := selectDescriptor(idx, target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcMusl})
		if err != nil {
			t.Fatalf("selectDescriptor: %v", err)
		}
		if desc.Annotations[AnnotationLibc] != "musl" {
			t.Errorf("selected libc = %q, want musl", desc.Annotations[AnnotationLibc])
		}
	})

	t.Run("unknown libc never guesses", func(t *testing.T) {
		_, err := selectDescriptor(idx, target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcUnknown})
		if !errors.Is(err, ErrNoPlatformMatch) {
			t.Errorf("err = %v, want ErrNoPlatformMatch", err)
		}
		if !strings.Contains(err.Error(), "DATAMITSU_LIBC") {
			t.Errorf("error %q should point at the DATAMITSU_LIBC override", err)
		}
	})

	t.Run("non-linux host ignores libc", func(t *testing.T) {
		desc, err := selectDescriptor(idx, target.Target{OS: "darwin", Arch: "arm64", Libc: target.LibcUnknown})
		if err != nil {
			t.Fatalf("selectDescriptor: %v", err)
		}
		if desc.Platform.OS != "darwin" {
			t.Errorf("selected OS = %q, want darwin", desc.Platform.OS)
		}
	})

	t.Run("no matching arch falls out", func(t *testing.T) {
		_, err := selectDescriptor(idx, target.Target{OS: "linux", Arch: "riscv64", Libc: target.LibcGlibc})
		if !errors.Is(err, ErrNoPlatformMatch) {
			t.Errorf("err = %v, want ErrNoPlatformMatch", err)
		}
	})
}

func TestSeed_IndexSelectionEndToEnd(t *testing.T) {
	storeRoot := testStore(t)
	host := target.HostTarget()
	if host.OS == "linux" && host.Libc == target.LibcUnknown {
		t.Skip("host libc unknown; index selection would intentionally refuse")
	}
	payload := []byte("from the index path")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": payload})

	src := newFakeSource()
	childDigest := src.addManifest(t, []ocispec.Descriptor{
		src.addLayer(binaryLayer(t, subtrees["tool"], payload), subtrees["tool"]),
	}, nil)

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
		Manifests: []ocispec.Descriptor{childDesc},
	}
	idxData, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	idxDigest := sha256DigestOf(idxData)
	src.manifests[idxDigest] = idxData

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", idxDigest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom via index: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool"]))); err != nil {
		t.Errorf("tool not seeded through the index path: %v", err)
	}
}

func TestSeed_NoPlatformMatchSurfacesTypedError(t *testing.T) {
	testStore(t)
	cfg, _ := testBinaryConfig(t, map[string][]byte{"tool": []byte("x")})

	src := newFakeSource()
	idx := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    godigest.Digest(sha256DigestOf([]byte("child"))),
			Platform:  &ocispec.Platform{OS: "plan9", Architecture: "mips"},
		}},
	}
	idxData, _ := json.Marshal(idx)
	idxDigest := sha256DigestOf(idxData)
	src.manifests[idxDigest] = idxData

	err := seedFrom(context.Background(), cfg, src, "test/bundle", idxDigest, nil, Options{})
	if !errors.Is(err, ErrNoPlatformMatch) {
		t.Fatalf("err = %v, want ErrNoPlatformMatch", err)
	}
	if IsFatalSeedError(err) {
		t.Error("a platform miss is a degradation, not an integrity failure")
	}
}

func TestAutoSeed_Gates(t *testing.T) {
	testStore(t)

	t.Run("nil config and nil oci are no-ops", func(t *testing.T) {
		if err := AutoSeed(context.Background(), nil, []string{"x"}, nil); err != nil {
			t.Errorf("AutoSeed(nil cfg) = %v", err)
		}
		if err := AutoSeed(context.Background(), &config.Config{}, []string{"x"}, nil); err != nil {
			t.Errorf("AutoSeed(no oci) = %v", err)
		}
	})

	cfgWithOCI := &config.Config{OCI: &config.OCIRef{
		Ref:    "registry.invalid/owner/repo",
		Digest: "sha256:" + strings.Repeat("ab", 32),
	}}

	t.Run("DATAMITSU_NO_OCI disables seeding", func(t *testing.T) {
		t.Setenv("DATAMITSU_NO_OCI", "1")
		if err := AutoSeed(context.Background(), cfgWithOCI, []string{"x"}, nil); err != nil {
			t.Errorf("AutoSeed under NO_OCI = %v, want nil (disabled)", err)
		}
	})

	t.Run("--no-oci flag disables seeding", func(t *testing.T) {
		SetDisabledByFlag(true)
		t.Cleanup(func() { SetDisabledByFlag(false) })
		if err := AutoSeed(context.Background(), cfgWithOCI, []string{"x"}, nil); err != nil {
			t.Errorf("AutoSeed under --no-oci = %v, want nil (disabled)", err)
		}
	})

	t.Run("offline never auto-pulls", func(t *testing.T) {
		t.Setenv("DATAMITSU_OFFLINE", "1")
		if err := AutoSeed(context.Background(), cfgWithOCI, []string{"x"}, nil); err != nil {
			t.Errorf("AutoSeed under offline = %v, want nil (skipped)", err)
		}
	})

	t.Run("empty needed set is a no-op", func(t *testing.T) {
		if err := AutoSeed(context.Background(), cfgWithOCI, nil, nil); err != nil {
			t.Errorf("AutoSeed with no targets = %v, want nil", err)
		}
	})
}

func TestSeedBundle_OfflineExplicitFails(t *testing.T) {
	testStore(t)
	t.Setenv("DATAMITSU_OFFLINE", "1")
	cfg := &config.Config{}
	ref := &config.OCIRef{Ref: "registry.invalid/owner/repo", Digest: "sha256:" + strings.Repeat("ab", 32)}

	err := SeedBundle(context.Background(), cfg, ref, Options{})
	if err == nil {
		t.Fatal("expected offline refusal, got nil")
	}
	if !strings.Contains(err.Error(), "DATAMITSU_OFFLINE") {
		t.Errorf("error %q should mention DATAMITSU_OFFLINE", err)
	}
}

func TestSeedFromLayout_EndToEnd(t *testing.T) {
	storeRoot := testStore(t)
	payload := []byte("layout payload")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": payload})

	// Build the layout from a fake source's content.
	src := newFakeSource()
	desc := src.addLayer(binaryLayer(t, subtrees["tool"], payload), subtrees["tool"])
	digest := src.addManifest(t, []ocispec.Descriptor{desc}, nil)

	layoutDir := t.TempDir()
	blobDir := filepath.Join(layoutDir, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlob := func(dgst string, data []byte) {
		if err := os.WriteFile(filepath.Join(blobDir, strings.TrimPrefix(dgst, "sha256:")), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeBlob(digest, src.manifests[digest])
	writeBlob(desc.Digest.String(), src.blobs[desc.Digest.String()])

	if err := SeedFromLayout(context.Background(), cfg, layoutDir, digest, nil, Options{}); err != nil {
		t.Fatalf("SeedFromLayout: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(storeRoot, filepath.FromSlash(subtrees["tool"])))
	if err != nil {
		t.Fatalf("seeded binary missing: %v", err)
	}
	if string(data) != string(payload) {
		t.Errorf("seeded content = %q, want %q", data, payload)
	}
}

func TestSeedFromLayout_TamperedBlobFails(t *testing.T) {
	testStore(t)
	payload := []byte("expected payload")
	cfg, subtrees := testBinaryConfig(t, map[string][]byte{"tool": payload})

	src := newFakeSource()
	desc := src.addLayer(binaryLayer(t, subtrees["tool"], payload), subtrees["tool"])
	digest := src.addManifest(t, []ocispec.Descriptor{desc}, nil)

	layoutDir := t.TempDir()
	blobDir := filepath.Join(layoutDir, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, strings.TrimPrefix(digest, "sha256:")), src.manifests[digest], 0o644); err != nil {
		t.Fatal(err)
	}
	// Tampered blob bytes under the original digest name.
	if err := os.WriteFile(filepath.Join(blobDir, strings.TrimPrefix(desc.Digest.String(), "sha256:")), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := SeedFromLayout(context.Background(), cfg, layoutDir, digest, nil, Options{})
	if err == nil {
		t.Fatal("expected tampered-blob failure, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error %q should mention the digest mismatch", err)
	}
}

func TestValidateSubtreeTable(t *testing.T) {
	valid := []string{
		".bin/golangci-lint/0123abcd",
		".runtimes/node/0123abcd",
		".apps/uv/cowsay/0123abcd",
		".uv/python",
		".uv/python/cpython-3.12",
	}
	for _, s := range valid {
		if err := validateSubtree(s); err != nil {
			t.Errorf("validateSubtree(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{
		"", "/abs", "../up", ".bin/../escape", "random/place", ".binx/evil",
		".bin/tool/../../../etc", "./bin/x",
	}
	for _, s := range invalid {
		if err := validateSubtree(s); err == nil {
			t.Errorf("validateSubtree(%q) = nil, want error", s)
		}
	}
}

func TestSeed_AbsoluteSymlinkOutsideStoreRootIsSkipped(t *testing.T) {
	storeRoot := testStore(t)
	cfg := &config.Config{}

	subtree := ".apps/uv/skipped-link/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	layer := subtreeLayer(t, subtree, []tarEntry{
		fileEntry(prefix+"/keep.txt", []byte("kept")),
		{name: prefix + "/escape", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}
	root := filepath.Join(storeRoot, filepath.FromSlash(subtree))
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Errorf("regular file should survive: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "escape")); err == nil {
		t.Error("absolute symlink outside the builder store root must be skipped")
	}
}

func TestSeed_RelocatedSymlinkTraversalIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}

	subtree := ".apps/uv/evil/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	layer := subtreeLayer(t, subtree, []tarEntry{
		{
			name: prefix + "/sneaky", typeflag: tar.TypeSymlink,
			linkname: testBuilderRoot + "/../../etc/passwd",
		},
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil {
		t.Fatal("expected traversal rejection, got nil")
	}
	if !strings.Contains(err.Error(), "escapes the store") {
		t.Errorf("error %q should mention the store escape", err)
	}
}

func TestSeed_RelativeSymlinkEscapingSubtreeIsSkipped(t *testing.T) {
	storeRoot := testStore(t)
	cfg := &config.Config{}

	subtree := ".apps/uv/relative-escape/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	layer := subtreeLayer(t, subtree, []tarEntry{
		fileEntry(prefix+"/keep.txt", []byte("kept")),
		{name: prefix + "/up", typeflag: tar.TypeSymlink, linkname: "../../../../outside"},
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}
	root := filepath.Join(storeRoot, filepath.FromSlash(subtree))
	if _, err := os.Lstat(filepath.Join(root, "up")); err == nil {
		t.Error("relative symlink escaping its subtree must be skipped")
	}
}

func TestSeed_HardlinkBeforeTargetIsRestored(t *testing.T) {
	storeRoot := testStore(t)
	cfg := &config.Config{}

	subtree := ".apps/node/ordering/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	// The hardlink entry precedes its target — legal in the tar format.
	layer := subtreeLayer(t, subtree, []tarEntry{
		{name: prefix + "/link.txt", typeflag: tar.TypeLink, linkname: prefix + "/original.txt"},
		fileEntry(prefix+"/original.txt", []byte("content")),
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}
	root := filepath.Join(storeRoot, filepath.FromSlash(subtree))
	origInfo, err := os.Stat(filepath.Join(root, "original.txt"))
	if err != nil {
		t.Fatal(err)
	}
	linkInfo, err := os.Stat(filepath.Join(root, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(origInfo, linkInfo) {
		t.Error("out-of-order hardlink was not restored")
	}
}

// testUVConfig builds a config with one managed uv runtime and one uv app for
// the host target, returning the expected subtrees of the app, its runtime,
// and the shared CPython.
func testUVConfig(t *testing.T) (*config.Config, map[string]string) {
	t.Helper()
	host := target.HostTarget()
	binaries := binmanager.MapOfBinaries{
		syslist.OsType(host.OS): {
			syslist.ArchType(host.Arch): {
				string(host.Libc): binmanager.BinaryOsArchInfo{
					URL:         "https://example.invalid/uv",
					Hash:        sha256Hex([]byte("uv binary")),
					ContentType: binmanager.BinContentTypeBinary,
				},
			},
		},
	}
	cfg := &config.Config{
		Runtimes: config.MapOfRuntimes{
			"uv": {
				Kind:    config.RuntimeKindUV,
				Mode:    config.RuntimeModeManaged,
				Managed: &config.RuntimeConfigManaged{Binaries: binaries},
			},
		},
		Apps: binmanager.MapOfApps{
			"mytool": {
				Required: true,
				Uv: &binmanager.AppConfigUV{
					PackageName: "mytool",
					Version:     "1.2.3",
					LockFile:    "lock-content",
				},
			},
		},
	}

	expected := expectedSubtrees(cfg, env.GetStorePath(), []string{"mytool"}, nil)
	return cfg, expected
}

func TestSeed_DemandDrivenPullsRuntimeDependencies(t *testing.T) {
	storeRoot := testStore(t)
	cfg, expected := testUVConfig(t)

	var appSubtree, runtimeSubtree string
	wantUVPython := false
	for subtree := range expected {
		switch {
		case strings.HasPrefix(subtree, ".apps/uv/mytool/"):
			appSubtree = subtree
		case strings.HasPrefix(subtree, ".runtimes/uv/"):
			runtimeSubtree = subtree
		case subtree == uvPythonSubtree:
			wantUVPython = true
		}
	}
	if appSubtree == "" || runtimeSubtree == "" || !wantUVPython {
		t.Fatalf("expected subtrees missing pieces: %v", expected)
	}

	src := newFakeSource()
	prefix := strings.TrimPrefix(testBuilderRoot, "/")
	appLayer := subtreeLayer(t, appSubtree, []tarEntry{fileEntry(prefix+"/"+appSubtree+"/marker", []byte("app"))})
	rtLayer := subtreeLayer(t, runtimeSubtree, []tarEntry{fileEntry(prefix+"/"+runtimeSubtree, []byte("uv binary"))})
	pyLayer := subtreeLayer(t, uvPythonSubtree, []tarEntry{fileEntry(prefix+"/"+uvPythonSubtree+"/cpython/bin/python3", []byte("py"))})
	unrelated := subtreeLayer(t, ".bin/unrelated/0123456789abcdef0123456789abcdef", []tarEntry{
		fileEntry(prefix+"/.bin/unrelated/0123456789abcdef0123456789abcdef", []byte("x")),
	})

	digest := src.addManifest(t, []ocispec.Descriptor{
		src.addLayer(appLayer, appSubtree),
		src.addLayer(rtLayer, runtimeSubtree),
		src.addLayer(pyLayer, uvPythonSubtree),
		src.addLayer(unrelated, ".bin/unrelated/0123456789abcdef0123456789abcdef"),
	}, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{Needed: []string{"mytool"}}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}

	if got := src.blobCalls.Load(); got != 3 {
		t.Errorf("blob calls = %d, want 3 (app + runtime + shared CPython)", got)
	}
	for _, subtree := range []string{appSubtree, runtimeSubtree, uvPythonSubtree} {
		if _, err := os.Stat(filepath.Join(storeRoot, filepath.FromSlash(subtree))); err != nil {
			t.Errorf("subtree %q not seeded: %v", subtree, err)
		}
	}
	if _, err := os.Stat(filepath.Join(storeRoot, ".bin", "unrelated")); err == nil {
		t.Error("unrelated layer was seeded")
	}
}

func TestRegistrySourceManifestDiskCache(t *testing.T) {
	testStore(t)
	body := []byte(`{"schemaVersion":2,"layers":[]}`)
	digest := sha256DigestOf(body)

	// Pre-seed the on-disk manifest cache, then point the source at an
	// unreachable registry: a cache hit must not touch the network at all.
	cachePath := manifestCachePath(digest)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	src := newRegistrySource("127.0.0.1:1", "owner/repo")
	got, err := src.manifest(context.Background(), digest)
	if err != nil {
		t.Fatalf("manifest from disk cache: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("cached manifest = %q, want %q", got, body)
	}

	// A corrupted cache entry must NOT be trusted: the source falls back to
	// the (unreachable) registry and fails instead of returning bad bytes.
	if err := os.WriteFile(cachePath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := src.manifest(context.Background(), digest); err == nil {
		t.Error("corrupted cache entry must not be served")
	}
}

// buildLayerZstd produces a zstd-compressed tar layer from entries.
func buildLayerZstd(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.name, Typeflag: e.typeflag, Mode: e.mode,
			Linkname: e.linkname, Size: int64(len(e.content)),
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestSeed_ZstdLayer(t *testing.T) {
	storeRoot := testStore(t)
	cfg := &config.Config{}

	subtree := ".bin/zstd-tool/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/")
	payload := []byte("zstd compressed payload")
	blob := buildLayerZstd(t, []tarEntry{
		dirEntry(prefix + "/"),
		dirEntry(prefix + "/.bin/"),
		dirEntry(prefix + "/.bin/zstd-tool/"),
		fileEntry(prefix+"/"+subtree, payload),
	})

	src := newFakeSource()
	dgst := sha256DigestOf(blob)
	src.blobs[dgst] = blob
	desc := ocispec.Descriptor{
		MediaType:   mediaTypeOCILayerZstd,
		Digest:      godigest.Digest(dgst),
		Size:        int64(len(blob)),
		Annotations: map[string]string{AnnotationSubtree: subtree},
	}
	digest := src.addManifest(t, []ocispec.Descriptor{desc}, nil)

	if err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("seedFrom: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(storeRoot, filepath.FromSlash(subtree)))
	if err != nil {
		t.Fatalf("zstd-seeded file missing: %v", err)
	}
	if string(data) != string(payload) {
		t.Errorf("content = %q, want %q", data, payload)
	}
}

func TestSeed_WhiteoutEntryIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}

	subtree := ".bin/wiped/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	layer := subtreeLayer(t, subtree, []tarEntry{
		fileEntry(prefix+"/.wh.deleted", []byte{}),
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "whiteout") {
		t.Fatalf("expected whiteout rejection, got %v", err)
	}
}

func TestSeed_UnsupportedEntryTypeIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}

	subtree := ".bin/fifo/0123456789abcdef0123456789abcdef"
	prefix := strings.TrimPrefix(testBuilderRoot, "/") + "/" + subtree
	layer := subtreeLayer(t, subtree, []tarEntry{
		{name: prefix + "/pipe", typeflag: tar.TypeFifo, mode: 0o644},
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "unsupported tar entry type") {
		t.Fatalf("expected unsupported-type rejection, got %v", err)
	}
}

func TestSeed_LayerWithoutSubtreeEntriesIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}

	subtree := ".bin/empty/0123456789abcdef0123456789abcdef"
	// Only structural parents, no actual subtree content.
	layer := buildLayer(t, []tarEntry{dirEntry("dm/"), dirEntry("dm/store/"), dirEntry("dm/store/.bin/")})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)}, nil)

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "contains no entries") {
		t.Fatalf("expected empty-subtree rejection, got %v", err)
	}
}

func TestSeed_RelativeBuilderStoreRootIsFatal(t *testing.T) {
	testStore(t)
	cfg := &config.Config{}

	subtree := ".bin/relative-root/0123456789abcdef0123456789abcdef"
	layer := subtreeLayer(t, subtree, []tarEntry{
		fileEntry(strings.TrimPrefix(testBuilderRoot, "/")+"/"+subtree, []byte("x")),
	})
	src := newFakeSource()
	digest := src.addManifest(t, []ocispec.Descriptor{src.addLayer(layer, subtree)},
		map[string]string{AnnotationStoreRoot: "dm/store"})

	err := seedFrom(context.Background(), cfg, src, "test/bundle", digest, nil, Options{})
	if err == nil || !IsFatalSeedError(err) {
		t.Fatalf("expected fatal invalid store-root error, got %v", err)
	}
}

func TestSeedBundle_MalformedRefIsRejected(t *testing.T) {
	testStore(t)
	for _, ref := range []*config.OCIRef{
		{Ref: "ghcr.io/owner/repo", Digest: "sha256:short"},
		{Ref: "owner-only", Digest: "sha256:" + strings.Repeat("ab", 32)},
	} {
		if err := SeedBundle(context.Background(), &config.Config{}, ref, Options{}); err == nil {
			t.Errorf("SeedBundle(%+v) = nil, want validation error", ref)
		}
	}
}
