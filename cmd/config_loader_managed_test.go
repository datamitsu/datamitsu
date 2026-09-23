package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
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
		{"unknown tool", `return { ...input, ejectConfigs: ["no-such-tool"] };`, `"no-such-tool" is not a configured tool`},
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

// A later layer receives earlier layers' entries through the Go-to-JS
// conversion that hides `content`, the same way a parent receives a
// getRemoteConfigs() result. Spreading them must keep the function, or every
// internal render of an inherited entry would fail the load.
func TestLoaderRendersAnEntryALaterLayerOnlyPassesThrough(t *testing.T) {
	isolateCacheTree(t)
	base := writeStandaloneConfig(t, ejectableBaseLayer)
	project := writeStandaloneConfig(t, `return { ...input, managedConfigs: { ...input.managedConfigs } };`)

	cfg, _, _ := loadCached(t, base, project)
	mc := cfg.ManagedConfigs[".gitleaks.toml"]
	if mc.Placement != config.PlacementInternal || mc.Render == nil || !strings.HasPrefix(mc.Render.Content, "placement=internal") {
		t.Fatalf("inherited entry: placement %q, render %+v", mc.Placement, mc.Render)
	}
}

// A remote layer's content function runs in the remote layer's own engine,
// which the chain no longer holds by the time internal renders happen. A clock
// read there must still keep the result out of the config-evaluation cache.
func TestLoaderObservesRemoteContentFunctionsItRenders(t *testing.T) {
	isolateCacheTree(t)
	remote := `function getConfig(input) { return {
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
