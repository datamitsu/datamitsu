package dockerfile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func sampleConfigForSlicing() (binmanager.MapOfApps, config.MapOfRuntimes, config.MapOfParsers) {
	apps := binmanager.MapOfApps{
		"shellcheck": {Binary: &binmanager.AppConfigBinary{Version: "0.11.0"}},
		"prettier":   {Node: &binmanager.AppConfigNode{PackageName: "prettier", Version: "3.8.3", Runtime: "node", BinPath: "bin/prettier.cjs"}},
		"ruff":       {Uv: &binmanager.AppConfigUV{PackageName: "ruff", Version: "0.15.0", Runtime: "uv"}},
	}
	runtimes := config.MapOfRuntimes{
		"node": {Kind: config.RuntimeKindNode, Node: &config.RuntimeConfigNode{NodeVersion: "22.12.0", PNPMVersion: "11.5.0", PNPMHash: "sha512-x"}},
		"uv":   {Kind: config.RuntimeKindUV, UV: &config.RuntimeConfigUV{PythonVersion: "3.13"}},
	}
	parsers := config.MapOfParsers{
		"core": {URL: "https://example.com/datamitsu_parsers_0.1.8.wasm", Hash: strings.Repeat("a", 64)},
	}
	return apps, runtimes, parsers
}

func findSlice(slices []Slice, file string) *Slice {
	for i := range slices {
		if slices[i].FileName == file {
			return &slices[i]
		}
	}
	return nil
}

func TestSliceFileName(t *testing.T) {
	if got := SliceFileName("app-prettier"); got != "app-prettier.js" {
		t.Errorf("SliceFileName = %q, want app-prettier.js", got)
	}
}

func TestBuildSlices_OnePerStageMinimal(t *testing.T) {
	apps, runtimes, parsers := sampleConfigForSlicing()
	plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})
	slices := BuildSlices(plan, apps, runtimes, parsers)

	// Runtime slice: only the runtime, no apps.
	rt := findSlice(slices, "rt-node.js")
	if rt == nil {
		t.Fatal("missing rt-node.js slice")
	}
	if len(rt.Config.Apps) != 0 {
		t.Errorf("runtime slice must carry no apps, got %v", rt.Config.Apps)
	}
	if _, ok := rt.Config.Runtimes["node"]; !ok || len(rt.Config.Runtimes) != 1 {
		t.Errorf("rt-node slice must carry exactly the node runtime, got %v", rt.Config.Runtimes)
	}

	// Binary slice: only the binary, no runtime.
	bin := findSlice(slices, "app-shellcheck.js")
	if bin == nil {
		t.Fatal("missing app-shellcheck.js slice")
	}
	if _, ok := bin.Config.Apps["shellcheck"]; !ok || len(bin.Config.Apps) != 1 {
		t.Errorf("binary slice must carry exactly shellcheck, got %v", bin.Config.Apps)
	}
	if len(bin.Config.Runtimes) != 0 {
		t.Errorf("binary slice must carry no runtime, got %v", bin.Config.Runtimes)
	}

	// Runtime-app slice: the app plus only its runtime — not sibling apps.
	app := findSlice(slices, "app-prettier.js")
	if app == nil {
		t.Fatal("missing app-prettier.js slice")
	}
	if _, ok := app.Config.Apps["prettier"]; !ok || len(app.Config.Apps) != 1 {
		t.Errorf("runtime-app slice must carry exactly prettier, got %v", app.Config.Apps)
	}
	if _, ok := app.Config.Runtimes["node"]; !ok || len(app.Config.Runtimes) != 1 {
		t.Errorf("runtime-app slice must carry exactly its node runtime, got %v", app.Config.Runtimes)
	}
	if _, leaked := app.Config.Apps["ruff"]; leaked {
		t.Error("prettier slice leaked a sibling app")
	}

	// Parser slice: exactly its `parsers` entry, no apps/runtimes.
	parser := findSlice(slices, "parser-core.js")
	if parser == nil {
		t.Fatal("missing parser-core.js slice")
	}
	if _, ok := parser.Config.Parsers["core"]; !ok || len(parser.Config.Parsers) != 1 {
		t.Errorf("parser slice must carry exactly the core parser, got %v", parser.Config.Parsers)
	}
	if len(parser.Config.Apps) != 0 || len(parser.Config.Runtimes) != 0 {
		t.Errorf("parser slice must carry no apps/runtimes, got apps=%v runtimes=%v", parser.Config.Apps, parser.Config.Runtimes)
	}
}

func TestRenderSlice_LoadableModuleRoundTrips(t *testing.T) {
	apps, runtimes, parsers := sampleConfigForSlicing()
	plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})
	slices := BuildSlices(plan, apps, runtimes, parsers)
	app := findSlice(slices, "app-prettier.js")
	if app == nil {
		t.Fatal("missing app-prettier.js slice")
	}

	js, err := RenderSlice(app.Config)
	if err != nil {
		t.Fatalf("RenderSlice: %v", err)
	}
	if !strings.Contains(js, "function getMinVersion()") || !strings.Contains(js, "function getConfig()") {
		t.Errorf("slice module missing required exports:\n%s", js)
	}

	// The embedded JSON must round-trip back to the same minimal config so a real
	// `install` sees prettier and its node runtime fully defined (lockfile, etc).
	const marker = "getConfig() { return "
	jsonStart := strings.Index(js, marker) + len(marker)
	jsonEnd := strings.LastIndex(js, "; }")
	var got config.Config
	if err := json.Unmarshal([]byte(js[jsonStart:jsonEnd]), &got); err != nil {
		t.Fatalf("embedded config is not valid JSON: %v", err)
	}
	if got.Apps["prettier"].Node == nil || got.Apps["prettier"].Node.PackageName != "prettier" {
		t.Errorf("round-tripped slice lost the prettier app def: %+v", got.Apps)
	}
	if got.Runtimes["node"].Node == nil || got.Runtimes["node"].Node.NodeVersion != "22.12.0" {
		t.Errorf("round-tripped slice lost the node runtime def: %+v", got.Runtimes)
	}
}
