package runtimemanager

import (
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func TestCollectRequiredRuntimesDependencies(t *testing.T) {
	for _, ref := range []string{"", "node"} {
		t.Run("runtime="+ref, func(t *testing.T) {
			apps := binmanager.MapOfApps{
				"root": {Lazy: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Required: true, Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}, DependsOn: edges("dep")},
				"dep":  {Required: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Lazy: true, DependsOn: edges("leaf"), Binary: &binmanager.AppConfigBinary{Binaries: nil, Version: ""}},
				"leaf": {Required: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Lazy: true, Node: &binmanager.AppConfigNode{PackageName: "", Version: "", BinPath: "", LockFile: "", Dependencies: nil, Runtime: ref}},
			}
			runtimes := config.MapOfRuntimes{
				"node":   {Mode: "", Managed: nil, System: nil, Bun: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindNode, Node: &config.RuntimeConfigNode{NodeVersion: "", PNPMRuntime: "pnpm"}},
				"pnpm":   {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindPNPM},
				"unused": {Mode: "", Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindUV},
			}
			got, err := CollectRequiredRuntimes(apps, runtimes, false)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"node", "pnpm"}; !slices.Equal(got, want) {
				t.Fatalf("runtimes = %v, want %v", got, want)
			}
			apps["dep"] = binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, DependsOn: edges("missing")}
			if _, err := CollectRequiredRuntimes(apps, runtimes, false); err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("error = %v, want missing dependency", err)
			}
		})
	}
}

func TestDependsOnPreservesInstallIdentity(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(config.MapOfRuntimes{"uv": {Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem}})
	app := binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Uv: &binmanager.AppConfigUV{RequiresPython: "", PackageName: "tool", Version: "1", LockFile: "lock", Runtime: "uv"}}
	before, err := rm.ComputeAppPath("tool", app)
	if err != nil {
		t.Fatal(err)
	}
	app.DependsOn = edges("dep")
	after, err := rm.ComputeAppPath("tool", app)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("dependency changed install path: %q -> %q", before, after)
	}
}

// edges builds a dependsOn edge list from plain names, mirroring the
// normalized string form of the config.
func edges(names ...string) []binmanager.AppDependency {
	out := make([]binmanager.AppDependency, 0, len(names))
	for _, n := range names {
		out = append(out, binmanager.AppDependency{App: n, Alias: n})
	}
	return out
}
