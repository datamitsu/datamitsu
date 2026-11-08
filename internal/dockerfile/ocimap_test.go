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

	// Python data follows runtime app layers and is emitted once per UV runtime.
	for _, entry := range m.Layers {
		if entry.Kind == "runtime" && entry.App == "go" {
			t.Error("build-only go runtime leaked into the oci map")
		}
	}
	wantKinds := []string{"runtime", "runtime", "app", "app", "python-data", "binary", "binary"}
	for i, kind := range wantKinds {
		if m.Layers[i].Kind != kind {
			t.Errorf("layer %d kind = %q, want %q (layers: %+v)", i, m.Layers[i].Kind, kind, m.Layers)
		}
	}
}

func TestPythonExportSourcesDeduplicatesByRuntime(t *testing.T) {
	plan := Plan{RuntimeAppStages: []RuntimeAppStage{
		{App: "z-tool", Kind: config.RuntimeKindUV, Runtime: "z-uv"},
		{App: "b-tool", Kind: config.RuntimeKindUV, Runtime: "a-uv"},
		{App: "a-tool", Kind: config.RuntimeKindUV, Runtime: "z-uv"},
		{App: "node-tool", Kind: config.RuntimeKindNode, Runtime: "node"},
	}}
	sources := pythonExportSources(plan)
	if len(sources) != 2 {
		t.Fatalf("Python export sources = %+v, want one per UV runtime", sources)
	}
	if sources[0].Runtime != "a-uv" || sources[0].App != "b-tool" || sources[1].Runtime != "z-uv" || sources[1].App != "a-tool" {
		t.Fatalf("Python export sources = %+v, want runtime-sorted lexicographic app owners", sources)
	}
	ociMap := BuildOCIMap(plan, DefaultRenderOptions())
	var pythonLayers []OCIMapEntry
	for _, entry := range ociMap.Layers {
		if entry.Kind == "python-data" {
			pythonLayers = append(pythonLayers, entry)
		}
	}
	if len(pythonLayers) != 2 || pythonLayers[0].Subtree != ".runtime-data/a-uv" || pythonLayers[1].Subtree != ".runtime-data/z-uv" {
		t.Fatalf("Python OCI layers = %+v, want one per sorted runtime", pythonLayers)
	}
	rendered := Render(plan, DefaultRenderOptions())
	if got := strings.Count(rendered, " AS python-export-"); got != 2 {
		t.Fatalf("Python export stages = %d, want 2", got)
	}
	if !strings.Contains(rendered, "FROM app-b-tool AS python-export-a-uv") || !strings.Contains(rendered, "FROM app-a-tool AS python-export-z-uv") {
		t.Fatalf("renderer did not choose the selected app stage per runtime:\n%s", rendered)
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
