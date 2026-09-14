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
				"root": {Required: true, Binary: &binmanager.AppConfigBinary{}, DependsOn: []string{"dep"}},
				"dep":  {Lazy: true, DependsOn: []string{"leaf"}, Binary: &binmanager.AppConfigBinary{}},
				"leaf": {Lazy: true, Node: &binmanager.AppConfigNode{Runtime: ref}},
			}
			runtimes := config.MapOfRuntimes{
				"node":   {Kind: config.RuntimeKindNode, Node: &config.RuntimeConfigNode{PNPMRuntime: "pnpm"}},
				"pnpm":   {Kind: config.RuntimeKindPNPM},
				"unused": {Kind: config.RuntimeKindUV},
			}
			got, err := CollectRequiredRuntimes(apps, runtimes, false)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"node", "pnpm"}; !slices.Equal(got, want) {
				t.Fatalf("runtimes = %v, want %v", got, want)
			}
			apps["dep"] = binmanager.App{DependsOn: []string{"missing"}}
			if _, err := CollectRequiredRuntimes(apps, runtimes, false); err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("error = %v, want missing dependency", err)
			}
		})
	}
}

func TestDependsOnPreservesInstallIdentity(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(config.MapOfRuntimes{"uv": {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem}})
	app := binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "tool", Version: "1", LockFile: "lock", Runtime: "uv"}}
	before, err := rm.ComputeAppPath("tool", app)
	if err != nil {
		t.Fatal(err)
	}
	app.DependsOn = []string{"dep"}
	after, err := rm.ComputeAppPath("tool", app)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("dependency changed install path: %q -> %q", before, after)
	}
}
