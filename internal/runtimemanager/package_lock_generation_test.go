package runtimemanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func TestPackageLockGenerationIsolatedFromPinnedInstallsAndPeers(t *testing.T) {
	helper := compileAppInstallHelper(t)
	for _, kind := range []string{"node", "bun", "uv"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("DATAMITSU_CACHE_DIR", base)
			t.Setenv("DATAMITSU_OFFLINE", "")
			t.Setenv("DATAMITSU_HELPER_PACKAGE", "probe-pkg")
			t.Setenv("DATAMITSU_APP_INSTALL_MODE", "lock")
			t.Setenv("DATAMITSU_HELPER_RECORD", filepath.Join(base, "record.json"))
			command := copyInstallerHelper(t, helper, filepath.Join(base, "bin", "installer"+executableSuffix()))
			manager := New(config.MapOfRuntimes{
				"node": {Kind: config.RuntimeKindNode, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: command, SystemVersion: "22"}, Node: &config.RuntimeConfigNode{NodeVersion: "22", PNPMRuntime: "pnpm"}},
				"bun":  {Kind: config.RuntimeKindBun, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: command, SystemVersion: "1.4"}, Bun: &config.RuntimeConfigBun{BunVersion: "1.4", PNPMRuntime: "pnpm"}},
				"pnpm": systemPNPMRuntime(command),
				"uv":   testUVRuntimeConfig(t, command, "3.14.6"),
			})
			pinnedLock := "lockfileVersion: '9.0'\nimporters: {}\n"
			if kind == "uv" {
				t.Setenv("DATAMITSU_HELPER_KIND", "uv")
				t.Setenv("UV_HELPER_PYTHON", seedTestPythonReady(t, manager, "uv", "", ""))
				pinnedLock = "version = 1\nrequires-python = \">=3.12\"\n"
			} else {
				t.Setenv("DATAMITSU_HELPER_KIND", "pnpm")
			}
			makeApp := func(lock string) binmanager.App {
				switch kind {
				case "node":
					return binmanager.App{Node: &binmanager.AppConfigNode{Runtime: "node", PackageName: "probe-pkg", Version: "1.2.3", BinPath: "bin/probe", LockFile: lock}}
				case "bun":
					return binmanager.App{Bun: &binmanager.AppConfigBun{Runtime: "bun", PackageName: "probe-pkg", Version: "1.2.3", BinPath: "bin/probe", LockFile: lock}}
				default:
					return binmanager.App{Uv: &binmanager.AppConfigUV{Runtime: "uv", PackageName: "probe-pkg", Version: "1.2.3", RequiresPython: ">=3.12", LockFile: lock}}
				}
			}
			app := makeApp(pinnedLock)
			pinnedPath, err := manager.ComputeAppPath("probe", app)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(pinnedPath, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pinnedPath, "preserve"), []byte("pinned installation"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := markAppReady(pinnedPath); err != nil {
				t.Fatal(err)
			}
			normalUnpinnedPath, err := manager.ComputeAppPath("probe", makeApp(""))
			if err != nil {
				t.Fatal(err)
			}
			type generated struct {
				content string
				err     error
			}
			results := make(chan generated, 2)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for range 2 {
				go func() {
					content, err := manager.GenerateLock(ctx, "probe", app)
					results <- generated{content: content, err: err}
				}()
			}
			workspaces := make(map[string]bool)
			completed := []generated{<-results, <-results}
			for _, result := range completed {
				if result.err != nil {
					t.Fatal(result.err)
				}
				workspace := ""
				for line := range strings.SplitSeq(result.content, "\n") {
					if after, ok := strings.CutPrefix(line, "# workspace="); ok {
						workspace = after
					}
				}
				if workspace == "" || workspace == pinnedPath || workspace == normalUnpinnedPath || workspaces[workspace] {
					t.Fatalf("lock generation reused an installed or peer workspace: %q", workspace)
				}
				workspaces[workspace] = true
				if _, err := os.Stat(workspace); !os.IsNotExist(err) {
					t.Fatalf("private lock workspace remained published: %v", err)
				}
			}
			if data, err := os.ReadFile(filepath.Join(pinnedPath, "preserve")); err != nil || string(data) != "pinned installation" || !appReady(pinnedPath) {
				t.Fatalf("lock generation changed the pinned installation: data=%q error=%v", data, err)
			}
			if _, err := os.Stat(normalUnpinnedPath); !os.IsNotExist(err) {
				t.Fatalf("lock generation published an unpinned app installation: %v", err)
			}
		})
	}
}
