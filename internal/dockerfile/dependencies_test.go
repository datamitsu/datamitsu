package dockerfile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func TestDependencySlices(t *testing.T) {
	for _, root := range []string{"shellcheck", "prettier"} {
		t.Run(root, func(t *testing.T) {
			apps, runtimes, parsers := sampleConfigForSlicing()
			apps["prettier"].Node.LockFile = "locked"
			apps["eslint"].Bun.LockFile = "locked"
			apps["ruff"].Uv.LockFile = "locked"
			apps["go-tool"].Go.LockFile = "locked"
			apps["dm-internal-upstream"] = binmanager.App{Binary: &binmanager.AppConfigBinary{}, Lazy: true}
			edges := map[string][]string{root: {"eslint", "ruff", "ktlint", "go-tool"}, "eslint": {"dm-internal-upstream"}, "ruff": {"dm-internal-upstream"}}
			for name, deps := range edges {
				app := apps[name]
				app.DependsOn = deps
				if name == "eslint" {
					app.RuntimeEnv = map[string]string{"UPSTREAM": "${APP_BIN:dm-internal-upstream}"}
				}
				apps[name] = app
			}
			plan := BuildPlan(apps, runtimes, PlanOptions{Parsers: parsers})
			all := mustBuildSlices(t, plan, apps, runtimes, parsers)
			for _, slice := range all {
				if _, err := config.ValidateApps(slice.Config.Apps, slice.Config.Runtimes); err != nil {
					t.Fatalf("%s: %v", slice.FileName, err)
				}
				if err := config.ValidateRuntimes(slice.Config.Runtimes); err != nil {
					t.Fatalf("%s: %v", slice.FileName, err)
				}
			}
			slice := findSlice(all, SliceFileName("app-"+root))
			names, err := binmanager.AppDependencyClosure(apps, []string{root})
			if err != nil {
				t.Fatal(err)
			}
			if len(slice.Config.Apps) != len(names) {
				t.Fatalf("apps = %v, want %v", slice.Config.Apps, names)
			}
			for _, name := range names {
				if !reflect.DeepEqual(slice.Config.Apps[name], apps[name]) {
					t.Errorf("app %q not preserved", name)
				}
			}
			for _, name := range []string{"bun", "uv", "jvm", "go", "pnpm"} {
				if !reflect.DeepEqual(slice.Config.Runtimes[name], runtimes[name]) {
					t.Errorf("runtime %q not preserved", name)
				}
			}
			rendered := Render(plan, pinnedOpts())
			for _, name := range names {
				mustContain(t, rendered, " install "+name)
			}
			mustContain(t, rendered, "COPY --link --from=app-dm-internal-upstream /dm/store/.bin/dm-internal-upstream /dm/store/.bin/dm-internal-upstream")
			mustContain(t, rendered, "COPY --link --from=app-eslint /dm/store/.apps/bun/eslint /dm/store/.apps/bun/eslint")
			mustContain(t, rendered, "COPY --link --from=rt-bun /dm/store/.runtimes/bun /dm/store/.runtimes/bun")
			ociMap := BuildOCIMap(plan, pinnedOpts())
			found := false
			for _, layer := range ociMap.Layers {
				if layer.App == "dm-internal-upstream" && layer.Subtree == ".bin/dm-internal-upstream" {
					found = true
				}
			}
			if !found {
				t.Fatal("OCI map omitted private dependency")
			}
			before, err := RenderSlice(slice.Config)
			if err != nil {
				t.Fatal(err)
			}
			dep := apps["dm-internal-upstream"]
			dep.Binary = &binmanager.AppConfigBinary{Version: "2"}
			apps["dm-internal-upstream"] = dep
			updated := mustBuildSlices(t, plan, apps, runtimes, parsers)
			after, err := RenderSlice(findSlice(updated, slice.FileName).Config)
			if err != nil {
				t.Fatal(err)
			}
			if before == after {
				t.Error("dependency change did not invalidate root slice")
			}
		})
	}
}

func TestPlanDependencyAvailability(t *testing.T) {
	for _, tt := range []struct {
		name       string
		dependency *binmanager.AppConfigBinary
		force      map[string]bool
		wantErr    string
	}{
		{name: "supported", dependency: linuxBin(map[syslist.ArchType][]string{"amd64": {"musl"}, "arm64": {"musl"}})},
		{name: "glibc only", dependency: linuxBin(map[syslist.ArchType][]string{"amd64": {"glibc"}}), wantErr: `dockerfile app "a-proxy" requires dependency "upstream": binary unavailable for the target Linux/libc variant`},
		{name: "one architecture lacks libc", dependency: linuxBin(map[syslist.ArchType][]string{"amd64": {"musl"}, "arm64": {"glibc"}}), wantErr: "upstream"},
		{name: "no linux", dependency: &binmanager.AppConfigBinary{}, wantErr: "target Linux/libc variant"},
		{name: "forcing root cannot hide missing dep", dependency: &binmanager.AppConfigBinary{}, force: map[string]bool{"a-proxy": true}, wantErr: "upstream"},
		{name: "explicit universal override", dependency: linuxBin(map[syslist.ArchType][]string{"amd64": {"glibc"}}), force: map[string]bool{"upstream": true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{
				"a-proxy":  {Node: &binmanager.AppConfigNode{Runtime: "node"}, DependsOn: []string{"middle"}},
				"middle":   {Binary: linuxBin(map[syslist.ArchType][]string{"amd64": {"musl"}}), DependsOn: []string{"upstream"}},
				"upstream": {Binary: tt.dependency},
			}
			runtimes := config.MapOfRuntimes{"node": {Kind: config.RuntimeKindNode}}
			plan := BuildPlan(apps, runtimes, PlanOptions{TargetLibc: "musl", ForceInclude: tt.force})
			err := plan.ValidateDependencies(apps)
			want := tt.wantErr
			if want == "" {
				if err != nil {
					t.Fatal(err)
				}
				mustContain(t, Render(plan, pinnedOpts()), "COPY --link --from=app-upstream /dm/store/.bin/upstream")
			} else {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want %q", err, want)
				}
				if _, err := BuildSlices(plan, apps, runtimes, nil); err == nil {
					t.Fatal("incomplete plan produced slices")
				}
			}
		})
	}
}

func TestSlicesRejectUnknownDependency(t *testing.T) {
	apps := binmanager.MapOfApps{"root": {Binary: &binmanager.AppConfigBinary{}, DependsOn: []string{"typo"}}}
	_, err := BuildSlices(BuildPlan(apps, nil), apps, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("error = %v", err)
	}
}
