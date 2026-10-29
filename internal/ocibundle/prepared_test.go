package ocibundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/prepare"
	"github.com/datamitsu/datamitsu/internal/storelock"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// preparedBinaryConfig extends the plain binary fixture with a prepare step.
func preparedBinaryConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := binaryReVerifyConfig(t)
	tool := cfg.Apps["tool"]
	off := false
	tool.Prepare = &binmanager.AppPrepare{
		Args:    []string{"--prepare"},
		Env:     map[string]string{"STEP_OUT": "${APP_PREPARED}"},
		Network: &off,
	}
	cfg.Apps["tool"] = tool
	return cfg
}

func TestSubtreeReadyPlainRootsNeedOnlyExist(t *testing.T) {
	storeRoot := testStore(t)
	dir := filepath.Join(storeRoot, ".bin", "tool", "abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if !subtreeReady(storeRoot, ".bin/tool/abc123") {
		t.Fatal("a plain content-addressed subtree must count as ready by stat alone")
	}
}

func TestSubtreeReadyPreparedNeedsMarker(t *testing.T) {
	storeRoot := testStore(t)
	state := filepath.Join(storeRoot, ".prepared", "tool", "abc123", "out")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	subtree := ".prepared/tool/abc123"
	if subtreeReady(storeRoot, subtree) {
		t.Fatal("a prepared state without its marker must not count as ready")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(state), prepare.MarkerName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !subtreeReady(storeRoot, subtree) {
		t.Fatal("a prepared state with its marker must count as ready")
	}
}

func TestAllSubtreesPresentMarkerlessStateForcesSeed(t *testing.T) {
	storeRoot := testStore(t)
	// A markerless state directory: the zero-network fast path must refuse it.
	state := filepath.Join(storeRoot, ".prepared", "tool", "abc123", "out")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{".prepared/tool/abc123": "prepared tool"}
	if allSubtreesPresent(storeRoot, expected) {
		t.Fatal("allSubtreesPresent accepted a markerless prepared state")
	}
	if err := os.WriteFile(filepath.Join(storeRoot, ".prepared", "tool", "abc123", prepare.MarkerName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !allSubtreesPresent(storeRoot, expected) {
		t.Fatal("allSubtreesPresent rejected a marked prepared state")
	}
}

func TestPlaceSubtreeDefersMarkerlessPreparedState(t *testing.T) {
	storeRoot := testStore(t)
	dest := filepath.Join(storeRoot, ".prepared", "tool", "abc123")
	// An interrupted placement: the directory exists, the marker does not.
	if err := os.MkdirAll(filepath.Join(dest, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "out", "partial.txt"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The staged replacement carries the marker.
	stagedRoot := t.TempDir()
	staged := filepath.Join(stagedRoot, "state")
	if err := os.MkdirAll(filepath.Join(staged, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "out", "data.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, prepare.MarkerName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := placeSubtree(staged, dest, ".prepared/tool/abc123"); err != nil {
		t.Fatalf("placeSubtree: %v", err)
	}
	if !prepare.Ready(dest) {
		t.Fatal("the complete staged state did not take the destination name")
	}
	if _, err := os.Stat(filepath.Join(dest, "out", "data.txt")); err != nil {
		t.Fatalf("placed state content: %v", err)
	}
	// The markerless remnant moved aside under tmp, not deleted in place.
	entries, err := os.ReadDir(filepath.Join(storeRoot, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	foundAside := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "stale-prepared-") {
			foundAside = true
			remnant := filepath.Join(storeRoot, "tmp", e.Name(), "state", "out", "partial.txt")
			if _, err := os.Stat(remnant); err != nil {
				t.Fatalf("aside remnant incomplete: %v", err)
			}
		}
	}
	if !foundAside {
		t.Fatalf("markerless state was not moved aside: %v", entries)
	}
}

func TestPlaceSubtreeKeepsExistingCompleteState(t *testing.T) {
	storeRoot := testStore(t)
	dest := filepath.Join(storeRoot, ".bin", "tool", "abc123")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(t.TempDir(), "staged")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	// Distinct content: the destination (no prepared.json, but NOT under
	// .prepared/) must win by the content-addressed rule, not be replaced.
	if err := os.WriteFile(filepath.Join(dest, "kept.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "kept.txt"), []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := placeSubtree(staged, dest, ".bin/tool/abc123"); err != nil {
		t.Fatalf("placeSubtree: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "kept.txt"))
	if err != nil || string(data) != "original" {
		t.Fatalf("destination content = %q, %v; the existing install tree must win untouched", data, err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("the staged copy was not dropped after losing to the existing destination")
	}
}

func TestExpectedSubtreesIncludesPreparedState(t *testing.T) {
	storeRoot := testStore(t)
	cfg := preparedBinaryConfig(t)
	expected := expectedSubtrees(cfg, storeRoot, []string{"tool"}, nil)

	var preparedOwner, preparedSubtree string
	for subtree, owner := range expected {
		if strings.HasPrefix(owner, "prepared ") {
			preparedOwner, preparedSubtree = owner, subtree
		}
	}
	if preparedOwner == "" {
		t.Fatalf("expected subtrees = %v, want a prepared entry", expected)
	}
	if preparedOwner != "prepared tool" {
		t.Errorf("prepared owner = %q", preparedOwner)
	}
	if !strings.HasPrefix(preparedSubtree, ".prepared/tool/") {
		t.Errorf("prepared subtree = %q, want a .prepared/tool/<key> path", preparedSubtree)
	}
	// The key is ComputePreparedKey: the same value the run path gates on.
	mgrs := newStoreManagers(cfg)
	key, err := mgrs.bm.ComputePreparedKey("tool")
	if err != nil {
		t.Fatal(err)
	}
	if preparedSubtree != ".prepared/tool/"+key {
		t.Errorf("prepared subtree = %q, want .prepared/tool/%s", preparedSubtree, key)
	}
}

func TestExpectedSubtreesNoStepNoPreparedEntry(t *testing.T) {
	storeRoot := testStore(t)
	cfg := binaryReVerifyConfig(t) // no prepare step
	for subtree := range expectedSubtrees(cfg, storeRoot, []string{"tool"}, nil) {
		if strings.HasPrefix(subtree, ".prepared/") {
			t.Fatalf("app without a step produced a prepared entry: %q", subtree)
		}
	}
}

func TestVerifyRequestedSameRootComparison(t *testing.T) {
	storeRoot := testStore(t)
	// Nothing seeded: both subtrees missing.
	expected := map[string]string{
		".bin/tool/abc123":      "tool",
		".prepared/tool/def456": "prepared tool",
	}
	// The bundle carries the app's install tree and a DIFFERENT prepared key.
	manifest := buildLayerManifest(t, []layerAnn{
		{subtree: ".bin/tool/999888", app: "tool"},
		{subtree: ".prepared/tool/otherkey", app: "tool"},
	})
	err := verifyRequested(storeRoot, expected, manifest)
	if err == nil {
		t.Fatal("verifyRequested accepted missing subtrees")
	}
	msg := err.Error()
	if !strings.Contains(msg, ".prepared/tool/otherkey") {
		t.Fatalf("mismatch report must name the same-root prepared subtree the bundle carries: %s", msg)
	}
	if !strings.Contains(msg, "this config differs") {
		t.Fatalf("a carried-but-different key must read as a config difference: %s", msg)
	}
}

type layerAnn struct {
	subtree string
	app     string
}

func buildLayerManifest(t *testing.T, layers []layerAnn) *ocispec.Manifest {
	t.Helper()
	m := &ocispec.Manifest{Layers: make([]ocispec.Descriptor, 0, len(layers))}
	for _, l := range layers {
		m.Layers = append(m.Layers, ocispec.Descriptor{
			Annotations: map[string]string{
				AnnotationSubtree: l.subtree,
				AnnotationApp:     l.app,
			},
		})
	}
	return m
}

// preparedStateLayer builds a bundle layer carrying a complete prepared state
// (out/ tree plus the readiness marker) under the given subtree.
func preparedStateLayer(t *testing.T, subtree string, payload []byte) []byte {
	t.Helper()
	prefix := strings.TrimPrefix(testBuilderRoot, "/")
	return subtreeLayer(t, subtree, []tarEntry{
		dirEntry(prefix + "/" + subtree + "/out"),
		fileEntry(prefix+"/"+subtree+"/out/data.txt", payload),
		fileEntry(prefix+"/"+subtree+"/"+prepare.MarkerName, []byte("{}")),
	})
}

// TestSeed_MarkerDeletionReSeedsPreparedState is the documented repair: the
// marker alone is deleted (an interrupted placement's trace), and the next
// seed recognizes the remnant as incomplete, moves it aside, and places the
// layer's complete state again.
func TestSeed_MarkerDeletionReSeedsPreparedState(t *testing.T) {
	storeRoot := testStore(t)
	cfg := preparedBinaryConfig(t)
	mgrs := newStoreManagers(cfg)
	key, err := mgrs.bm.ComputePreparedKey("tool")
	if err != nil {
		t.Fatal(err)
	}
	subtree := ".prepared/tool/" + key

	src := newFakeSource()
	desc := src.addLayer(preparedStateLayer(t, subtree, []byte("state payload")), subtree)
	digest := src.addManifest(t, []ocispec.Descriptor{desc}, nil)

	opts := Options{Needed: []string{"tool"}}
	if _, err := seedFromChanged(context.Background(), cfg, src, "test/bundle", digest, nil, opts); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	state := filepath.Join(storeRoot, filepath.FromSlash(subtree))
	if !prepare.Ready(state) {
		t.Fatal("first seed did not leave a ready prepared state")
	}

	// The repair: only the marker is deleted.
	if err := os.Remove(filepath.Join(state, prepare.MarkerName)); err != nil {
		t.Fatal(err)
	}

	// The second seed must not treat the markerless remnant as satisfied.
	if _, err := seedFromChanged(context.Background(), cfg, src, "test/bundle", digest, nil, opts); err != nil {
		t.Fatalf("second seed (repair): %v", err)
	}
	if !prepare.Ready(state) {
		t.Fatal("second seed did not restore the prepared state")
	}
	data, err := os.ReadFile(filepath.Join(state, "out", "data.txt"))
	if err != nil || string(data) != "state payload" {
		t.Fatalf("restored state content = %q, %v", data, err)
	}
}

// TestSeed_FullPullRepairsMarkerDeletion is the full-seed twin of the
// demand-driven repair: the bundle marker records the prepared subtree, so a
// deleted prepared.json must make markerSatisfied stale and the second FULL
// pull must re-place the layer.
func TestSeed_FullPullRepairsMarkerDeletion(t *testing.T) {
	storeRoot := testStore(t)
	cfg := preparedBinaryConfig(t)
	mgrs := newStoreManagers(cfg)
	key, err := mgrs.bm.ComputePreparedKey("tool")
	if err != nil {
		t.Fatal(err)
	}
	subtree := ".prepared/tool/" + key

	src := newFakeSource()
	desc := src.addLayer(preparedStateLayer(t, subtree, []byte("state payload")), subtree)
	digest := src.addManifest(t, []ocispec.Descriptor{desc}, nil)

	// Full pull (Options{}): writes the bundle marker.
	if _, err := seedFromChanged(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("first full pull: %v", err)
	}
	state := filepath.Join(storeRoot, filepath.FromSlash(subtree))
	if !prepare.Ready(state) {
		t.Fatal("first full pull did not leave a ready prepared state")
	}
	if !markerSatisfied(storeRoot, digest) {
		t.Fatal("bundle marker not satisfied right after the full pull")
	}

	// The repair: only the marker is deleted.
	if err := os.Remove(filepath.Join(state, prepare.MarkerName)); err != nil {
		t.Fatal(err)
	}
	if markerSatisfied(storeRoot, digest) {
		t.Fatal("markerSatisfied accepted a store whose recorded prepared state lost its marker")
	}

	if _, err := seedFromChanged(context.Background(), cfg, src, "test/bundle", digest, nil, Options{}); err != nil {
		t.Fatalf("second full pull (repair): %v", err)
	}
	if !prepare.Ready(state) {
		t.Fatal("second full pull did not restore the prepared state")
	}
}

// TestSeedAndPrepareShareTheLock proves the serialized pairing: while the
// prepare engine's lock is HELD on a state directory, a seed of that subtree
// blocks (it must not move a remnant aside under a running placement); once
// released, the seed proceeds.
func TestSeedAndPrepareShareTheLock(t *testing.T) {
	storeRoot := testStore(t)
	cfg := preparedBinaryConfig(t)
	mgrs := newStoreManagers(cfg)
	key, err := mgrs.bm.ComputePreparedKey("tool")
	if err != nil {
		t.Fatal(err)
	}
	subtree := ".prepared/tool/" + key

	storeAbs, err := filepath.Abs(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(storeAbs, filepath.FromSlash(subtree))

	// The prepare engine's lock, exactly as internal/prepare takes it.
	release, err := storelock.Lock(context.Background(), storeAbs, stateDir)
	if err != nil {
		t.Fatal(err)
	}

	seeded := make(chan error, 1)
	go func() {
		src := newFakeSource()
		desc := src.addLayer(preparedStateLayer(t, subtree, []byte("raced")), subtree)
		digest := src.addManifest(t, []ocispec.Descriptor{desc}, nil)
		_, err := seedFromChanged(context.Background(), cfg, src, "test/bundle", digest, nil, Options{Needed: []string{"tool"}})
		seeded <- err
	}()

	select {
	case err := <-seeded:
		release()
		t.Fatalf("seed completed while the prepare lock was held: %v", err)
	case <-time.After(300 * time.Millisecond):
		// Still blocked: the locks serialize. Release and let it finish.
	}

	release()
	select {
	case err := <-seeded:
		if err != nil {
			t.Fatalf("seed after lock release: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("seed did not finish after the prepare lock was released")
	}
	if !prepare.Ready(stateDir) {
		t.Fatal("seeded prepared state not ready after the coordinated run")
	}
}
