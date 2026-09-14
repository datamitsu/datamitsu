package sourcefarm

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func TestPlanDependencyHealthAndRuntimeEnv(t *testing.T) {
	for _, installed := range []bool{false, true} {
		name := "missing"
		if installed {
			name = "installed"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			t.Setenv("DATAMITSU_OFFLINE", "1")
			osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
			if err != nil {
				t.Fatal(err)
			}
			arch, err := syslist.GetArchTypeFromString(runtime.GOARCH)
			if err != nil {
				t.Fatal(err)
			}
			binary := &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{osType: {arch: {"unknown": {URL: "https://example.invalid/tool", Hash: "0000000000000000000000000000000000000000000000000000000000000000", ContentType: binmanager.BinContentTypeBinary}}}}}
			apps := binmanager.MapOfApps{
				"root": {Binary: binary, DependsOn: []string{"git", "dep"}, RuntimeEnv: map[string]string{"UPSTREAM": "${APP_BIN:git}"}},
				"dep":  {Binary: binary, DependsOn: []string{"git"}}, "git": {Binary: binary, Lazy: true},
			}
			bm := binmanager.New(apps, nil, nil)
			dependency, _, err := bm.ResolveCommandInfo("git")
			if err != nil {
				t.Fatal(err)
			}
			for _, appName := range []string{"root", "dep", "git"} {
				if !installed && appName == "git" {
					continue
				}
				info, _, err := bm.ResolveCommandInfo(appName)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(info.Command), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(info.Command, []byte("stub"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			before := binmanager.NetworkDownloads()
			plan := BuildPlan(t.TempDir(), "", apps, bm, nil)
			if len(plan.Entries) != 2 || len(plan.Excluded) != 1 || plan.Excluded[0].Name != "git" {
				t.Fatalf("excluded = %v", plan.Excluded)
			}
			for _, entry := range plan.Entries {
				if entry.Name != "root" {
					continue
				}
				if !slices.Contains(entry.RequiredPaths, dependency.Command) || entry.Installed != installed {
					t.Fatalf("entry = %+v", entry)
				}
				if entry.Env["UPSTREAM"] != dependency.Command {
					t.Fatalf("env = %v", entry.Env)
				}
				if len(entry.RequiredPaths) != 2 {
					t.Fatalf("diamond health paths duplicated: %v", entry.RequiredPaths)
				}
			}
			if binmanager.NetworkDownloads() != before {
				t.Fatal("plan downloaded")
			}
		})
	}
}
