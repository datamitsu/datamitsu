package dockerfile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func samplePNPMRuntime(version string) config.RuntimeConfig {
	binaryPath := "pnpm"
	return config.RuntimeConfig{
		Kind: config.RuntimeKindPNPM,
		Mode: config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{Binaries: binmanager.MapOfBinaries{
			"linux": {"amd64": {"glibc": {URL: "https://example.com/pnpm-linux-x64.tar.gz", Hash: strings.Repeat("b", 64), ContentType: binmanager.BinContentTypeTarGz, BinaryPath: &binaryPath, ExtractDir: true}}},
		}},
		PNPM: &config.RuntimeConfigPNPM{PNPMVersion: version},
	}
}

func sampleConfigForSlicing() (binmanager.MapOfApps, config.MapOfRuntimes, config.MapOfParsers) {
	apps := binmanager.MapOfApps{
		"shellcheck": {Binary: &binmanager.AppConfigBinary{Version: "0.11.0"}},
		"prettier":   {Node: &binmanager.AppConfigNode{PackageName: "prettier", Version: "3.8.3", Runtime: "node", BinPath: "bin/prettier.cjs"}},
		"eslint":     {Bun: &binmanager.AppConfigBun{PackageName: "eslint", Version: "10.9.0", Runtime: "bun", BinPath: "node_modules/eslint/bin/eslint.js"}},
		"ruff":       {Uv: &binmanager.AppConfigUV{PackageName: "ruff", Version: "0.15.0", Runtime: "uv"}},
		"ktlint":     {Jvm: &binmanager.AppConfigJVM{JarURL: "https://example.com/ktlint.jar", JarHash: strings.Repeat("c", 64), Version: "1.5.0", Runtime: "jvm"}},
		"go-tool":    {Go: &binmanager.AppConfigGo{PackageName: "example.com/go-tool", Version: "1.0.0", Runtime: "go"}},
	}
	runtimes := config.MapOfRuntimes{
		"node": {Kind: config.RuntimeKindNode, Node: &config.RuntimeConfigNode{NodeVersion: "22.12.0", PNPMRuntime: "pnpm"}},
		"bun":  {Kind: config.RuntimeKindBun, Bun: &config.RuntimeConfigBun{BunVersion: "1.4.1", PNPMRuntime: "pnpm"}},
		"uv":   {Kind: config.RuntimeKindUV, UV: &config.RuntimeConfigUV{PythonVersion: "3.13"}},
		"jvm":  {Kind: config.RuntimeKindJVM, JVM: &config.RuntimeConfigJVM{JavaVersion: "21"}},
		"go":   {Kind: config.RuntimeKindGo, Go: &config.RuntimeConfigGo{GoVersion: "1.26.3"}},
		"pnpm": samplePNPMRuntime("12.4.1"),
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

// TestBuildSlices_EverySliceLoads pins what the generated Dockerfile depends on:
// every stage loads only its own slice, and a config load validates each runtime
// reference in it — a Node or Bun slice without its pnpm runtime would fail to
// load and take the whole build down with it.
func TestBuildSlices_EverySliceLoads(t *testing.T) {
	apps, runtimes, parsers := sampleConfigForSlicing()
	plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})

	for _, s := range BuildSlices(plan, apps, runtimes, parsers) {
		if err := config.ValidateRuntimes(s.Config.Runtimes); err != nil {
			t.Errorf("stage %s cannot load its slice: %v", s.StageName, err)
		}
	}
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
	_, rtHasNode := rt.Config.Runtimes["node"]
	_, rtHasPNPM := rt.Config.Runtimes["pnpm"]
	if !rtHasNode || !rtHasPNPM || len(rt.Config.Runtimes) != 2 {
		t.Errorf("rt-node slice must carry the node runtime and the pnpm runtime it references, got %v", rt.Config.Runtimes)
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

	// Runtime-app slice: the app plus only the runtimes it installs with — not
	// sibling apps. A Node app installs through its pnpm runtime.
	app := findSlice(slices, "app-prettier.js")
	if app == nil {
		t.Fatal("missing app-prettier.js slice")
	}
	if _, ok := app.Config.Apps["prettier"]; !ok || len(app.Config.Apps) != 1 {
		t.Errorf("runtime-app slice must carry exactly prettier, got %v", app.Config.Apps)
	}
	_, hasNode := app.Config.Runtimes["node"]
	_, hasPNPM := app.Config.Runtimes["pnpm"]
	if !hasNode || !hasPNPM || len(app.Config.Runtimes) != 2 {
		t.Errorf("runtime-app slice must carry exactly its node and pnpm runtimes, got %v", app.Config.Runtimes)
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
	if got.Runtimes["pnpm"].PNPM == nil || got.Runtimes["pnpm"].PNPM.PNPMVersion != "12.4.1" {
		t.Errorf("round-tripped slice lost the pnpm runtime def: %+v", got.Runtimes)
	}
	// Without its pnpm runtime the node runtime's pnpmRuntime would dangle and
	// the stage's config would not load.
	if err := config.ValidateRuntimes(got.Runtimes); err != nil {
		t.Errorf("the slice a build stage loads fails validation: %v", err)
	}
}

// TestBuildSlices_PNPMRuntimeOnlyForNodeAndBun pins which app slices carry a
// pnpm runtime: Node and Bun apps install through the one their runtime names,
// every other kind needs none.
func TestBuildSlices_PNPMRuntimeOnlyForNodeAndBun(t *testing.T) {
	apps, runtimes, parsers := sampleConfigForSlicing()
	plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})
	slices := BuildSlices(plan, apps, runtimes, parsers)

	tests := []struct {
		file     string
		runtime  string
		wantPNPM bool
	}{
		{"app-prettier.js", "node", true},
		{"app-eslint.js", "bun", true},
		{"app-ruff.js", "uv", false},
		{"app-ktlint.js", "jvm", false},
		{"app-go-tool.js", "go", false},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			s := findSlice(slices, tt.file)
			if s == nil {
				t.Fatalf("missing %s slice", tt.file)
			}
			if _, ok := s.Config.Runtimes[tt.runtime]; !ok {
				t.Errorf("slice lost its %s runtime: %v", tt.runtime, s.Config.Runtimes)
			}
			if _, got := s.Config.Runtimes["pnpm"]; got != tt.wantPNPM {
				t.Errorf("slice carries the pnpm runtime = %v, want %v (runtimes %v)", got, tt.wantPNPM, s.Config.Runtimes)
			}
		})
	}
}

// TestBuildSlices_PNPMRuntimeFollowsReference checks that a Node app slice gets
// the pnpm runtime its runtime names, not whichever one sorts first.
func TestBuildSlices_PNPMRuntimeFollowsReference(t *testing.T) {
	apps, runtimes, parsers := sampleConfigForSlicing()
	runtimes["pnpm-alt"] = samplePNPMRuntime("12.3.4")
	node := runtimes["node"]
	node.Node = &config.RuntimeConfigNode{NodeVersion: "22.12.0", PNPMRuntime: "pnpm-alt"}
	runtimes["node"] = node

	plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})
	s := findSlice(BuildSlices(plan, apps, runtimes, parsers), "app-prettier.js")
	if s == nil {
		t.Fatal("missing app-prettier.js slice")
	}
	if _, ok := s.Config.Runtimes["pnpm-alt"]; !ok {
		t.Errorf("slice lacks the referenced pnpm-alt runtime: %v", s.Config.Runtimes)
	}
	if _, leaked := s.Config.Runtimes["pnpm"]; leaked {
		t.Errorf("slice carries the unreferenced pnpm runtime: %v", s.Config.Runtimes)
	}
}

// TestRenderSlice_OCIParserRoundTrips covers the registry-sourced declaration
// reaching a buildkit stage. The slice is what `datamitsu install --config`
// loads inside the build, so the oci pin has to survive marshaling intact —
// otherwise the stage would try to fetch a module with no source at all.
func TestRenderSlice_OCIParserRoundTrips(t *testing.T) {
	apps, runtimes, _ := sampleConfigForSlicing()
	parsers := config.MapOfParsers{
		"core": {
			Hash: strings.Repeat("a", 64),
			OCI: &config.ParserOCI{
				Ref:    "ghcr.io/datamitsu/datamitsu-parsers",
				Digest: "sha256:" + strings.Repeat("b", 64),
			},
		},
	}
	plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})
	if got := plan.RegistrySourcedParsers; len(got) != 1 || got[0] != "core" {
		t.Errorf("RegistrySourcedParsers = %v, want [core] so the CLI can warn about buildkit egress", got)
	}

	slices := BuildSlices(plan, apps, runtimes, parsers)
	parserSlice := findSlice(slices, "parser-core.js")
	if parserSlice == nil {
		t.Fatal("missing parser-core.js slice")
	}
	js, err := RenderSlice(parserSlice.Config)
	if err != nil {
		t.Fatalf("RenderSlice: %v", err)
	}

	const marker = "getConfig() { return "
	jsonStart := strings.Index(js, marker) + len(marker)
	jsonEnd := strings.LastIndex(js, "; }")
	var got config.Config
	if err := json.Unmarshal([]byte(js[jsonStart:jsonEnd]), &got); err != nil {
		t.Fatalf("embedded config is not valid JSON: %v", err)
	}
	p := got.Parsers["core"]
	if p.OCI == nil || p.OCI.Ref != "ghcr.io/datamitsu/datamitsu-parsers" {
		t.Fatalf("round-tripped slice lost the parser's oci source: %+v", p)
	}
	if p.URL != "" {
		t.Errorf("round-tripped slice grew a url source: %q", p.URL)
	}
	if err := config.ValidateParsers(got.Parsers); err != nil {
		t.Errorf("the slice a build stage loads fails validation: %v", err)
	}
}

// TestBuildPlan_URLParserIsNotFlaggedAsRegistrySourced keeps the warning quiet
// for the default configuration, where the parser is an ordinary HTTPS
// download and buildkit needs no registry at all.
func TestBuildPlan_URLParserIsNotFlaggedAsRegistrySourced(t *testing.T) {
	apps, runtimes, parsers := sampleConfigForSlicing()
	plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})
	if got := plan.RegistrySourcedParsers; len(got) != 0 {
		t.Errorf("RegistrySourcedParsers = %v, want empty for a url-sourced parser", got)
	}
}
