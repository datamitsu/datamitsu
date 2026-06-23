package ocibundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestLayoutSource_BlobPathRejectsBadDigests(t *testing.T) {
	s := newLayoutSource(t.TempDir())
	for _, bad := range []string{
		"md5:" + strings.Repeat("ab", 16),
		"sha256:short",
		"sha256:" + strings.Repeat("zz", 32), // not hex
	} {
		if _, err := s.blobPath(bad); err == nil {
			t.Errorf("blobPath(%q) = nil, want error", bad)
		}
	}
	good := "sha256:" + strings.Repeat("ab", 32)
	if _, err := s.blobPath(good); err != nil {
		t.Errorf("blobPath(%q) = %v, want nil", good, err)
	}
}

func TestLayoutSource_ManifestMissingFile(t *testing.T) {
	s := newLayoutSource(t.TempDir())
	_, err := s.manifest(context.Background(), sha256DigestOf([]byte("absent")))
	if err == nil {
		t.Fatal("expected error reading an absent manifest blob")
	}
}

func TestLayoutSource_ManifestVerifiesAndOversize(t *testing.T) {
	dir := t.TempDir()
	blobDir := filepath.Join(dir, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"schemaVersion":2}`)
	digest := sha256DigestOf(body)
	if err := os.WriteFile(filepath.Join(blobDir, sha256Hex(body)), body, 0o644); err != nil {
		t.Fatal(err)
	}

	s := newLayoutSource(dir)
	got, err := s.manifest(context.Background(), digest)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("manifest = %q, want %q", got, body)
	}

	// Bytes stored under a digest they do not hash to must be rejected.
	wrongDigest := sha256DigestOf([]byte("different"))
	if err := os.WriteFile(filepath.Join(blobDir, sha256Hex([]byte("different"))), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.manifest(context.Background(), wrongDigest); err == nil {
		t.Error("manifest with mismatched content must fail verification")
	}
}

func TestLayoutSource_BlobMissingFile(t *testing.T) {
	s := newLayoutSource(t.TempDir())
	desc := ocispec.Descriptor{Digest: godigest.Digest("sha256:" + strings.Repeat("ab", 32)), Size: 1}
	if _, err := s.blob(context.Background(), desc, t.TempDir(), "x"); err == nil {
		t.Fatal("expected error reading an absent blob file")
	}
}

func TestLayoutSource_BlobSizeMismatch(t *testing.T) {
	dir := t.TempDir()
	blobDir := filepath.Join(dir, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("payload bytes")
	if err := os.WriteFile(filepath.Join(blobDir, sha256Hex(payload)), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	s := newLayoutSource(dir)
	desc := ocispec.Descriptor{Digest: godigest.Digest("sha256:" + sha256Hex(payload)), Size: int64(len(payload)) + 99}
	if _, err := s.blob(context.Background(), desc, t.TempDir(), "x"); err == nil {
		t.Fatal("expected size-mismatch error")
	}
}

func TestSeedFromLayout_MissingDirectory(t *testing.T) {
	testStore(t)
	err := SeedFromLayout(context.Background(), &config.Config{},
		filepath.Join(t.TempDir(), "does-not-exist"), sha256DigestOf([]byte("x")), nil, Options{})
	if err == nil {
		t.Fatal("expected error for a missing layout directory")
	}
}

func TestSeedFromLayout_PathIsNotADirectory(t *testing.T) {
	testStore(t)
	file := filepath.Join(t.TempDir(), "regular.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := SeedFromLayout(context.Background(), &config.Config{}, file, sha256DigestOf([]byte("x")), nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", err)
	}
}

func TestPlaceSubtree_DestinationAlreadyExistsDropsStaged(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "staged")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	// Destination already present: placeSubtree must drop the staged copy
	// (the content-addressed winner already holds identical content).
	if err := placeSubtree(staged, dest); err != nil {
		t.Fatalf("placeSubtree: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("staged copy should have been removed after losing the race")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("destination should remain: %v", err)
	}
}

func TestPlaceSubtree_RenamesIntoFreshDest(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "staged")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "f"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "nested", "dest")

	if err := placeSubtree(staged, dest); err != nil {
		t.Fatalf("placeSubtree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "f")); err != nil {
		t.Errorf("renamed content missing: %v", err)
	}
}

func TestRemoveStaged(t *testing.T) {
	staged := filepath.Join(t.TempDir(), "staged")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeStaged(staged); err != nil {
		t.Fatalf("removeStaged: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("removeStaged did not remove the directory")
	}
}

func TestWriteMarker_MkdirFailure(t *testing.T) {
	// A regular file where the store root must hold the marker directory makes
	// MkdirAll fail.
	base := t.TempDir()
	storeRoot := filepath.Join(base, "store")
	if err := os.WriteFile(storeRoot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeMarker(storeRoot, "ref", sha256DigestOf([]byte("x")), []string{".bin/x"})
	if err == nil {
		t.Fatal("expected marker mkdir failure when the store root is a file")
	}
}

func TestRuntimeAppKind(t *testing.T) {
	cases := []struct {
		name    string
		app     binmanager.App
		wantOK  bool
		wantRef config.RuntimeKind
	}{
		{"uv", binmanager.App{Uv: &binmanager.AppConfigUV{Runtime: "uv"}}, true, config.RuntimeKindUV},
		{"node", binmanager.App{Node: &binmanager.AppConfigNode{Runtime: "node"}}, true, config.RuntimeKindNode},
		{"jvm", binmanager.App{Jvm: &binmanager.AppConfigJVM{Runtime: "jvm"}}, true, config.RuntimeKindJVM},
		{"go", binmanager.App{Go: &binmanager.AppConfigGo{Runtime: "go"}}, true, config.RuntimeKindGo},
		{"binary not a runtime app", binmanager.App{Binary: &binmanager.AppConfigBinary{}}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, _, ok := runtimeAppKind(tc.app)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && kind != tc.wantRef {
				t.Errorf("kind = %q, want %q", kind, tc.wantRef)
			}
		})
	}
}

func TestSubtreeRel_OutsideStoreRootRejected(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	if _, err := subtreeRel(store, store); err == nil {
		t.Error("subtreeRel of the store root itself should fail")
	}
	if _, err := subtreeRel(store, filepath.Join(filepath.Dir(store), "elsewhere")); err == nil {
		t.Error("subtreeRel of a sibling path should fail")
	}
	rel, err := subtreeRel(store, filepath.Join(store, ".bin", "tool", "hash"))
	if err != nil {
		t.Fatalf("subtreeRel of an in-store path: %v", err)
	}
	if rel != ".bin/tool/hash" {
		t.Errorf("rel = %q, want .bin/tool/hash", rel)
	}
}

// binaryReVerifyConfig builds a config with one single-file binary app for the
// host target so buildReVerifyIndex can resolve and index it.
func binaryReVerifyConfig(t *testing.T) *config.Config {
	t.Helper()
	host := target.HostTarget()
	return &config.Config{
		Apps: binmanager.MapOfApps{
			"tool": {
				Required: true,
				Binary: &binmanager.AppConfigBinary{
					Binaries: binmanager.MapOfBinaries{
						syslist.OsType(host.OS): {
							syslist.ArchType(host.Arch): {
								string(host.Libc): binmanager.BinaryOsArchInfo{
									URL:         "https://example.invalid/tool",
									Hash:        sha256Hex([]byte("tool payload")),
									ContentType: binmanager.BinContentTypeBinary,
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestBuildReVerifyIndex_IndexesBinaryApp(t *testing.T) {
	storeRoot := testStore(t)
	cfg := binaryReVerifyConfig(t)

	index := buildReVerifyIndex(cfg, storeRoot)
	if len(index) != 1 {
		t.Fatalf("index entries = %d, want 1 (the single-file binary)", len(index))
	}
	for _, spec := range index {
		if spec.owner != "tool" {
			t.Errorf("owner = %q, want tool", spec.owner)
		}
		if spec.sha256 != sha256Hex([]byte("tool payload")) {
			t.Errorf("sha256 = %q, want the published hash", spec.sha256)
		}
		if spec.relPath != "" {
			t.Errorf("relPath = %q, want empty for a single-file binary", spec.relPath)
		}
	}
}

func TestExpectedSubtrees_BinaryAppPath(t *testing.T) {
	storeRoot := testStore(t)
	cfg := binaryReVerifyConfig(t)

	expected := expectedSubtrees(cfg, storeRoot, []string{"tool"}, nil)
	if len(expected) != 1 {
		t.Fatalf("expected subtrees = %v, want exactly one for the binary app", expected)
	}
	for subtree, owner := range expected {
		if owner != "tool" {
			t.Errorf("owner = %q, want tool", owner)
		}
		if !strings.HasPrefix(subtree, ".bin/tool/") {
			t.Errorf("subtree = %q, want a .bin/tool/ path", subtree)
		}
	}
}

func TestExpectedSubtrees_UnknownAndShellAppsContributeNothing(t *testing.T) {
	storeRoot := testStore(t)
	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"shellapp": {Shell: &binmanager.AppConfigShell{}},
		},
	}
	// "unknown" is not in cfg.Apps; "shellapp" is a shell app — both skipped.
	expected := expectedSubtrees(cfg, storeRoot, []string{"unknown", "shellapp"}, nil)
	if len(expected) != 0 {
		t.Errorf("expected = %v, want empty (unknown + shell apps contribute nothing)", expected)
	}
}
