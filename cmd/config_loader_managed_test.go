package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const ejectableBaseLayer = `
	return {
		tools: { gitleaks: { name: "gitleaks", operations: { lint: {
			app: "gitleaks", args: ["--config", "{managedConfig:.gitleaks.toml}", "{target}"],
		} } } },
		managedConfigs: { ".gitleaks.toml": {
			scope: "git-root", tools: ["gitleaks"], ejectable: true,
			content: (ctx) => "placement=" + ctx.placement + " from=" + ctx.datamitsuDirFromOutput + "\n",
		} },
	};`

func TestLoaderRendersAnInternalConfigAndCachesIt(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, ejectableBaseLayer)

	for _, load := range []string{"miss", "hit"} {
		cfg, _, vm := loadCached(t, path)
		if (load == "hit") != servedFromCache(vm) {
			t.Fatalf("%s: servedFromCache = %v", load, servedFromCache(vm))
		}
		mc := cfg.ManagedConfigs[".gitleaks.toml"]
		if mc.Placement != config.PlacementInternal {
			t.Fatalf("%s: placement = %q", load, mc.Placement)
		}
		if mc.Render == nil || mc.Render.Content != "placement=internal from=..\n" {
			t.Fatalf("%s: render = %+v", load, mc.Render)
		}
		op := cfg.Tools["gitleaks"].Operations[config.OpLint]
		if !slices.Equal(op.Args, []string{"--config", "{root}/.datamitsu/configs/.gitleaks.toml", "{target}"}) {
			t.Errorf("%s: args = %v", load, op.Args)
		}
		if len(op.ManagedConfigRefs) != 1 || op.ManagedConfigRefs[0].Key != ".gitleaks.toml" {
			t.Errorf("%s: refs = %v", load, op.ManagedConfigRefs)
		}
	}
}

func TestLoaderEjectsFromALaterLayer(t *testing.T) {
	isolateCacheTree(t)
	base := writeStandaloneConfig(t, ejectableBaseLayer)
	project := writeStandaloneConfig(t, `return { ...input, ejectConfigs: ["gitleaks"] };`)

	cfg, _, _ := loadCached(t, base, project)
	mc := cfg.ManagedConfigs[".gitleaks.toml"]
	if mc.Placement != config.PlacementRepo || mc.Render != nil {
		t.Fatalf("ejected entry: placement %q, render %+v", mc.Placement, mc.Render)
	}
	if got := cfg.Tools["gitleaks"].Operations[config.OpLint].Args[1]; got != "{root}/.gitleaks.toml" {
		t.Errorf("ejected path = %q", got)
	}
}

func TestLoaderRejectsInvalidEjectDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		project string
		want    string
	}{
		{"dangling managed config tool", `return { ...input, managedConfigs: { ...input.managedConfigs, "x.yml": { tools: ["nope"], content: () => "" } } };`, `unknown tool "nope"`},
		{"placeholder naming no config", `return { ...input, tools: { ...input.tools, other: { name: "other", operations: { lint: { app: "x", args: ["{managedConfig:missing.toml}"] } } } } };`, "names no managed config"},
		{"content that throws", `return { ...input, managedConfigs: { ".gitleaks.toml": { scope: "git-root", tools: ["gitleaks"], ejectable: true, content: () => { throw new Error("boom"); } } } };`, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateCacheTree(t)
			base := writeStandaloneConfig(t, ejectableBaseLayer)
			project := writeStandaloneConfig(t, tt.project)
			_, _, _, err := loadConfigWithPaths(t.Context(), nil, true, []string{base, project})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("load error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// An ejectConfigs name the pinned configuration does not define is wrapper
// version drift, not a broken chain: the load succeeds, the placement of every
// entry is the internal one, and the warning replays on a cache hit.
func TestLoaderWarnsOnAnUnknownEjectName(t *testing.T) {
	isolateCacheTree(t)
	observed := swapLoggerWithObserver(t, zapcore.WarnLevel)
	base := writeStandaloneConfig(t, ejectableBaseLayer)
	project := writeStandaloneConfig(t, `return { ...input, ejectConfigs: ["no-such-tool"] };`)

	want := `ejectConfigs: "no-such-tool" is not a configured tool`
	for _, load := range []string{"miss", "hit"} {
		cfg, _, vm := loadCached(t, base, project)
		if (load == "hit") != servedFromCache(vm) {
			t.Fatalf("%s: servedFromCache = %v", load, servedFromCache(vm))
		}
		if cfg.ManagedConfigs[".gitleaks.toml"].Placement != config.PlacementInternal {
			t.Errorf("%s: placement = %q, want internal", load, cfg.ManagedConfigs[".gitleaks.toml"].Placement)
		}
		if got := countLogsWith(observed, want); got != 1 {
			t.Errorf("%s: %d warnings containing %q", load, got, want)
		}
		observed.TakeAll()
	}
}

func countLogsWith(observed *observer.ObservedLogs, substr string) int {
	n := 0
	for _, entry := range observed.TakeAll() {
		if strings.Contains(entry.Message, substr) {
			n++
		}
	}
	return n
}

// A later layer receives earlier layers' entries through the Go-to-JS conversion
// that hides `content`. Cloning the entry must preserve its executable callback.
func TestLoaderRendersAnEntryALaterLayerOnlyPassesThrough(t *testing.T) {
	cases := []struct {
		name  string
		layer string
	}{
		{name: "outer and map spread", layer: `return { ...input, managedConfigs: { ...input.managedConfigs } };`},
		{name: "deep merge", layer: `return merge(input, {});`},
		{name: "entry spread", layer: `return { ...input, managedConfigs: { ...input.managedConfigs, ".gitleaks.toml": { ...input.managedConfigs[".gitleaks.toml"] } } };`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCacheTree(t)
			base := writeStandaloneConfig(t, ejectableBaseLayer)
			project := writeStandaloneConfig(t, tc.layer)
			cfg, _, _ := loadCached(t, base, project)
			mc := cfg.ManagedConfigs[".gitleaks.toml"]
			if mc.Placement != config.PlacementInternal || mc.Render == nil || !strings.HasPrefix(mc.Render.Content, "placement=internal") {
				t.Fatalf("inherited entry: placement %q, render %+v", mc.Placement, mc.Render)
			}
		})
	}
}

func TestLoaderInheritedContentSemantics(t *testing.T) {
	cases := []struct {
		name         string
		layers       []string
		baseResult   string
		want         string
		wantError    string
		absent       bool
		renderAbsent bool
	}{
		{name: "project replacement", layers: []string{`return { ...input, managedConfigs: { ...input.managedConfigs, ".gitleaks.toml": { ...input.managedConfigs[".gitleaks.toml"], content: () => "project" } } };`}, want: "project"},
		{name: "removed entry", layers: []string{`return { ...input, tools: {}, managedConfigs: {} };`}, absent: true},
		{name: "undefined opt out", layers: []string{`return merge(input, {});`}, baseResult: "undefined", renderAbsent: true},
		{name: "null opt out", layers: []string{`return merge(input, {});`}, baseResult: "null", renderAbsent: true},
		{name: "throwing callback", layers: []string{`return merge(input, {});`}, baseResult: `(() => { throw new Error("inherited-content-failure"); })()`, wantError: "inherited-content-failure"},
		{name: "nested callback bridge", layers: []string{`return merge(input, {});`, `return merge(input, {});`}, want: "base:placement=internal from=..\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCacheTree(t)
			baseBody := strings.Replace(ejectableBaseLayer, `return {`, `const inheritedValue = "base"; return {`, 1)
			baseBody = strings.Replace(baseBody, `"placement="`, `inheritedValue + ":placement="`, 1)
			if tc.renderAbsent {
				baseBody = strings.Replace(baseBody, `ejectable: true`, `ejectable: false`, 1)
			}
			if tc.baseResult != "" {
				baseBody = strings.Replace(baseBody, `content: (ctx) => inheritedValue + ":placement=" + ctx.placement + " from=" + ctx.datamitsuDirFromOutput + "\n",`, `content: (ctx) => `+tc.baseResult+`,`, 1)
			}
			base := writeStandaloneConfig(t, baseBody)
			paths := []string{base}
			for _, layer := range tc.layers {
				paths = append(paths, writeStandaloneConfig(t, layer))
			}
			cfg, _, _, err := loadConfigWithPaths(context.Background(), nil, true, paths)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			mc, ok := cfg.ManagedConfigs[".gitleaks.toml"]
			if tc.absent {
				if ok {
					t.Fatalf("removed entry remained: %+v", mc)
				}
				return
			}
			if !ok {
				t.Fatal("managed entry disappeared")
			}
			if tc.renderAbsent {
				if mc.Render != nil {
					t.Fatalf("opt-out rendered content: %+v", mc.Render)
				}
				return
			}
			if mc.Render == nil || mc.Render.Content != tc.want {
				t.Fatalf("render = %+v, want %q", mc.Render, tc.want)
			}
		})
	}
}

// A remote layer's content function runs in the remote layer's own engine,
// which the chain no longer holds by the time internal renders happen. A clock
// read there must still keep the result out of the config-evaluation cache.
func TestLoaderObservesRemoteContentFunctionsItRenders(t *testing.T) {
	isolateCacheTree(t)
	remote := `function getConfig(input) { return {
		version: "1.0.0",
		tools: { gitleaks: { name: "gitleaks", operations: { lint: { app: "gitleaks", args: ["{managedConfig:.gitleaks.toml}"] } } } },
		managedConfigs: { ".gitleaks.toml": { scope: "git-root", tools: ["gitleaks"], ejectable: true, content: () => String(Date.now()) } },
	}; }`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(remote))
	}))
	defer server.Close()

	local := filepath.Join(t.TempDir(), "datamitsu.config.js")
	writeFile(t, local, fmt.Sprintf(`
function getMinVersion() { return "0.0.0"; }
function getRemoteConfigs() { return [{ url: %q, hash: %q }]; }
function getConfig(input) { return { ...input }; }`, server.URL+"/remote.js", computeHash(remote)))

	cfg, _, _ := loadCached(t, local)
	if cfg.ManagedConfigs[".gitleaks.toml"].Render == nil {
		t.Fatal("the remote entry was not rendered")
	}
	if configEvalCacheable() {
		t.Fatal("a render that read the clock in a remote layer was left cacheable")
	}
}
