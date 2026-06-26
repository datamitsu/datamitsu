package dockerfile

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func sampleParsers() config.MapOfParsers {
	return config.MapOfParsers{
		"core":   {URL: "https://example.com/datamitsu_parsers_0.1.8.wasm", Hash: strings.Repeat("a", 64)},
		"legacy": {URL: "https://example.com/datamitsu_parsers_0.1.7.wasm", Hash: strings.Repeat("b", 64)},
	}
}

func TestBuildPlan_ParserStagesSorted(t *testing.T) {
	plan := BuildPlan(nil, nil, PlanOptions{Parsers: sampleParsers()})
	if len(plan.ParserStages) != 2 {
		t.Fatalf("ParserStages = %d, want 2", len(plan.ParserStages))
	}
	// Sorted for deterministic rendering.
	if plan.ParserStages[0].Module != "core" || plan.ParserStages[1].Module != "legacy" {
		t.Errorf("parser stages not sorted: %+v", plan.ParserStages)
	}

	// No parsers in opts → no parser stages (the bundle-without-parsers default).
	if got := BuildPlan(nil, nil); len(got.ParserStages) != 0 {
		t.Errorf("ParserStages should be empty without PlanOptions.Parsers, got %+v", got.ParserStages)
	}
}

func TestBuildOCIMap_ParserEntriesAfterBinaries(t *testing.T) {
	plan := Plan{
		BinaryStages: []BinaryStage{{App: "shellcheck"}},
		ParserStages: []ParserStage{{Module: "core"}},
	}
	opts := DefaultRenderOptions()
	m := BuildOCIMap(plan, opts)

	if len(m.Layers) != 2 {
		t.Fatalf("layers = %d, want 2", len(m.Layers))
	}
	// Parsers come after binaries.
	bin, parser := m.Layers[0], m.Layers[1]
	if bin.Kind != "binary" {
		t.Errorf("layer 0 kind = %q, want binary", bin.Kind)
	}
	if parser.Kind != "parser" || parser.Subtree != ".parsers/core" || parser.App != "core" {
		t.Errorf("parser layer = %+v, want kind=parser subtree=.parsers/core app=core", parser)
	}
}

func TestRender_ParserStageAndFinalCopy(t *testing.T) {
	plan := BuildPlan(nil, nil, PlanOptions{Parsers: config.MapOfParsers{
		"core": {URL: "https://example.com/m.wasm", Hash: strings.Repeat("a", 64)},
	}})
	opts := DefaultRenderOptions()
	opts.BaseImage = "ghcr.io/datamitsu/datamitsu:1.0.0"
	out := Render(plan, opts)

	for _, want := range []string{
		"FROM dm-base AS parser-core",
		"devtools parsers prefetch",
		"COPY --link --from=config-split /slices/parser-core.js",
		"COPY --link --from=parser-core /dm/store/.parsers/core /dm/store/.parsers/core",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered Dockerfile missing %q\n---\n%s", want, out)
		}
	}
}

// The oci-map must mirror the final stage's COPY order even with parser layers,
// so the post-process maps layers→subtrees correctly.
func TestBuildOCIMap_MirrorsRenderWithParsers(t *testing.T) {
	plan := Plan{
		BinaryStages: []BinaryStage{{App: "shellcheck"}},
		ParserStages: []ParserStage{{Module: "core"}, {Module: "legacy"}},
	}
	opts := DefaultRenderOptions()
	opts.BaseImage = "ghcr.io/datamitsu/datamitsu:1.0.0"
	m := BuildOCIMap(plan, opts)

	var rendered []string
	for line := range strings.Lines(Render(plan, opts)) {
		if !strings.HasPrefix(line, "COPY --link --from=") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		dest := fields[len(fields)-1]
		if subtree, ok := strings.CutPrefix(dest, m.StoreRoot+"/"); ok {
			rendered = append(rendered, subtree)
		}
	}
	if len(rendered) != len(m.Layers) {
		t.Fatalf("rendered %d store COPYs, map has %d layers", len(rendered), len(m.Layers))
	}
	for i, entry := range m.Layers {
		if entry.Subtree != rendered[i] {
			t.Errorf("layer %d: map subtree %q, rendered COPY %q", i, entry.Subtree, rendered[i])
		}
	}
}
