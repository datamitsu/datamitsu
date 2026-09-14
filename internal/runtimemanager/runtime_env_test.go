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
		{config.RuntimeKindBun, binmanager.App{Bun: &binmanager.AppConfigBun{BinPath: "index.js", PackageName: "tool"}}},
		{config.RuntimeKindNode, binmanager.App{Node: &binmanager.AppConfigNode{BinPath: "tool", PackageName: "tool"}}},
		{config.RuntimeKindUV, binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "tool"}}},
		{config.RuntimeKindGo, binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "example.org/tool"}}},
		{config.RuntimeKindJVM, binmanager.App{Jvm: &binmanager.AppConfigJVM{Version: "1"}}},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			rm := New(config.MapOfRuntimes{string(tt.kind): {Kind: tt.kind, Mode: config.RuntimeModeSystem}, "pnpm": {Kind: config.RuntimeKindPNPM, Mode: config.RuntimeModeSystem}})
			before, err := rm.ComputeAppPath("tool", tt.app)
			if err != nil {
				t.Fatal(err)
			}
			tt.app.RuntimeEnv = map[string]string{"BIN": "${APP_BIN:dep}"}
			after, err := rm.ComputeAppPath("tool", tt.app)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("runtimeEnv changed install identity: %s -> %s", before, after)
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
			rm := New(config.MapOfRuntimes{"uv": {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: uv}}})
			capture := filepath.Join(dir, "capture")
			app := binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "tool", Version: "1"}, Env: map[string]string{"DEP_TEST_INSTALL_ENV": "install", "DEP_TEST_CAPTURE": capture}, RuntimeEnv: map[string]string{"DEP_TEST_RUNTIME_ONLY": runtimeValue}}
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
