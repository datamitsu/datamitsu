package runtimemanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func TestRuntimeEnvPreservesAppIdentity(t *testing.T) {
	for _, tt := range []struct {
		kind config.RuntimeKind
		app  binmanager.App
	}{
		{config.RuntimeKindBun, binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Uv: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Bun: &binmanager.AppConfigBun{Version: "", Runtime: "", LockFile: "", Dependencies: nil, BinPath: "index.js", PackageName: "tool"}}},
		{config.RuntimeKindNode, binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Node: &binmanager.AppConfigNode{Version: "", Runtime: "", LockFile: "", Dependencies: nil, BinPath: "tool", PackageName: "tool"}}},
		{config.RuntimeKindUV, binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Uv: &binmanager.AppConfigUV{Version: "", Runtime: "", LockFile: "", RequiresPython: "", PackageName: "tool"}}},
		{config.RuntimeKindGo, binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Jvm: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Go: &binmanager.AppConfigGo{Version: "", Runtime: "", LockFile: "", PackageName: "example.org/tool"}}},
		{config.RuntimeKindJVM, binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Uv: nil, Node: nil, Go: nil, Shell: nil, Env: nil, RuntimeEnv: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Jvm: &binmanager.AppConfigJVM{JarURL: "", JarHash: "", Runtime: "", MainClass: "", Version: "1"}}},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			rm := New(config.MapOfRuntimes{string(tt.kind): {Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: tt.kind, Mode: config.RuntimeModeSystem}, "pnpm": {Managed: nil, System: nil, Bun: nil, Node: nil, UV: nil, JVM: nil, Go: nil, PNPM: nil, Kind: config.RuntimeKindPNPM, Mode: config.RuntimeModeSystem}})
			before, err := rm.ComputeAppPath("tool", tt.app)
			if err != nil {
				t.Fatal(err)
			}
			tt.app.RuntimeEnv = map[string]string{"BIN": "${APP_BIN:dep}"}
			tt.app.Caches = []string{"providers"}
			after, err := rm.ComputeAppPath("tool", tt.app)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("execution cache settings changed install identity: %s -> %s", before, after)
			}
		})
	}
}

func TestRuntimeEnvNotPassedToInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture needs a POSIX shell")
	}
	for _, runtimeValue := range []string{"execution-only", "${APP_BIN:dep}"} {
		t.Run(runtimeValue, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			t.Setenv("DATAMITSU_OFFLINE", "")
			t.Setenv("DEP_TEST_RUNTIME_ONLY", "")
			dir := t.TempDir()
			uv := filepath.Join(dir, "uv")
			script := `#!/bin/sh
set -eu
printf '%s|%s' "$DEP_TEST_INSTALL_ENV" "$DEP_TEST_RUNTIME_ONLY" > "$DEP_TEST_CAPTURE"
mkdir -p .venv/bin
printf '#!/bin/sh\n' > .venv/bin/tool
printf '#!/bin/sh\n' > .venv/bin/python
chmod +x .venv/bin/tool .venv/bin/python
`
			if err := os.WriteFile(uv, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			rm := New(config.MapOfRuntimes{"uv": testUVRuntimeConfig(t, uv, "3.14.0")})
			seedTestPythonReady(t, rm, "uv", "", "")
			capture := filepath.Join(dir, "capture")
			app := binmanager.App{Required: false, Lazy: false, Description: "", VersionCheck: nil, DependsOn: nil, OfficialURL: "", OfficialURLDerived: false, Binary: nil, Bun: nil, Node: nil, Jvm: nil, Go: nil, Shell: nil, Caches: nil, Files: nil, Links: nil, Archives: nil, Prepare: nil, Uv: &binmanager.AppConfigUV{Runtime: "", LockFile: "", RequiresPython: "", PackageName: "tool", Version: "1"}, Env: map[string]string{"DEP_TEST_INSTALL_ENV": "install", "DEP_TEST_CAPTURE": capture}, RuntimeEnv: map[string]string{"DEP_TEST_RUNTIME_ONLY": runtimeValue}}
			if _, err := rm.GetCommandInfo(context.Background(), "tool", app); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(got)) != "install|" {
				t.Fatalf("installer saw %q", got)
			}
		})
	}
}
