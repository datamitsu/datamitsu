package dockerfile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func testOCIMapPlan() Plan {
	return Plan{
		RuntimeStages: []RuntimeStage{
			{Name: "go", Kind: config.RuntimeKindGo}, // build-only: excluded from the final image
			{Name: "node", Kind: config.RuntimeKindNode},
			{Name: "uv", Kind: config.RuntimeKindUV},
		},
		RuntimeAppStages: []RuntimeAppStage{
			{App: "cowsay", Kind: config.RuntimeKindUV, Runtime: "uv"},
			{App: "prettier", Kind: config.RuntimeKindNode, Runtime: "node"},
		},
		BinaryStages: []BinaryStage{
			{App: "golangci-lint"},
			{App: "shellcheck"},
		},
	}
}

func TestBuildOCIMapMirrorsRenderedCopyOrder(t *testing.T) {
	plan := testOCIMapPlan()
	opts := DefaultRenderOptions()
	opts.BaseImage = "ghcr.io/datamitsu/datamitsu:1.0.0"

	m := BuildOCIMap(plan, opts)

	if m.Version != 1 {
		t.Errorf("Version = %d, want 1", m.Version)
	}
	if m.StoreRoot != "/dm/store" {
		t.Errorf("StoreRoot = %q, want /dm/store", m.StoreRoot)
	}

	// The map must mirror the final stage's per-subtree COPY --link emission
	// order exactly — extract that order from the rendered Dockerfile so the
	// two cannot drift apart silently.
	var rendered []string
	for line := range strings.Lines(Render(plan, opts)) {
		if !strings.HasPrefix(line, "COPY --link --from=") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		dest := fields[len(fields)-1]
		subtree, ok := strings.CutPrefix(dest, m.StoreRoot+"/")
		if !ok {
			continue // the config COPY, not a store subtree layer
		}
		rendered = append(rendered, subtree)
	}

	if len(rendered) != len(m.Layers) {
		t.Fatalf("rendered %d store COPYs, map has %d layers", len(rendered), len(m.Layers))
	}
	for i, entry := range m.Layers {
		if entry.Subtree != rendered[i] {
			t.Errorf("layer %d: map subtree %q, rendered COPY %q", i, entry.Subtree, rendered[i])
		}
	}

	// The Go runtime is build-only and must not appear; uv apps carry the
	// shared CPython entry right after their own layer.
	for _, entry := range m.Layers {
		if entry.Kind == "runtime" && entry.App == "go" {
			t.Error("build-only go runtime leaked into the oci map")
		}
	}
	wantKinds := []string{"runtime", "runtime", "app", "uv-python", "app", "binary", "binary"}
	for i, kind := range wantKinds {
		if m.Layers[i].Kind != kind {
			t.Errorf("layer %d kind = %q, want %q (layers: %+v)", i, m.Layers[i].Kind, kind, m.Layers)
		}
	}
}

func TestMarshalOCIMapIsStable(t *testing.T) {
	plan := testOCIMapPlan()
	opts := DefaultRenderOptions()

	first, err := MarshalOCIMap(BuildOCIMap(plan, opts))
	if err != nil {
		t.Fatalf("MarshalOCIMap: %v", err)
	}
	second, err := MarshalOCIMap(BuildOCIMap(plan, opts))
	if err != nil {
		t.Fatalf("MarshalOCIMap: %v", err)
	}
	if string(first) != string(second) {
		t.Error("MarshalOCIMap is not deterministic")
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Error("MarshalOCIMap output must end with a newline")
	}

	var decoded OCIMap
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(decoded.Layers) != 7 {
		t.Errorf("decoded %d layers, want 7", len(decoded.Layers))
	}
}
