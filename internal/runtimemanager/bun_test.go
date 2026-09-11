package runtimemanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
)

func bunSystemRuntime(command string) config.MapOfRuntimes {
	return config.MapOfRuntimes{
		"bun": {
			Kind:   config.RuntimeKindBun,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: command, SystemVersion: "1.4.1"},
			Bun: &config.RuntimeConfigBun{
				BunVersion:  "1.4.1",
				PNPMVersion: "11.20.0",
				PNPMHash:    "test-pnpm-hash",
			},
		},
	}
}

func seedBunTestPNPM(t *testing.T) string {
	t.Helper()
	pnpmPath := env.GetPNPMPath(env.GetStorePath(), "11.20.0", "test-pnpm-hash")
	if err := os.MkdirAll(filepath.Dir(pnpmPath), 0o755); err != nil {
		t.Fatalf("mkdir pnpm dir: %v", err)
	}
	if err := os.WriteFile(pnpmPath, []byte("// fake pnpm\n"), 0o644); err != nil {
		t.Fatalf("write fake pnpm: %v", err)
	}
	return pnpmPath
}

func TestResolveBunCommandInfo(t *testing.T) {
	storeRoot := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", storeRoot)
	rm := New(bunSystemRuntime("bun"))
	app := &binmanager.AppConfigBun{
		PackageName: "eslint",
		Version:     "10.9.0",
		BinPath:     "node_modules/eslint/bin/eslint.js",
		LockFile:    "lock",
	}

	info, err := rm.resolveBunCommandInfo("eslint", app, nil, nil)
	if err != nil {
		t.Fatalf("resolveBunCommandInfo() error = %v", err)
	}
	if info.Type != "bun" || info.Command != "bun" {
		t.Errorf("command info = type %q command %q, want bun/bun", info.Type, info.Command)
	}
	if len(info.Args) != 6 || info.Args[0] != "--config="+os.DevNull || !slices.Equal(info.Args[1:5], []string{"--no-env-file", "run", "--bun", "--no-install"}) || !strings.HasSuffix(info.Args[5], filepath.FromSlash(app.BinPath)) {
		t.Errorf("Args = %v, want ambient-config guards plus run --bun --no-install and the app entrypoint", info.Args)
	}
	if got := info.Env["npm_config_store_dir"]; got != env.GetPNPMStorePath() {
		t.Errorf("npm_config_store_dir = %q, want %q", got, env.GetPNPMStorePath())
	}
	appEnvPath, err := rm.resolveBunAppEnvPath("eslint", app, nil, nil)
	if err != nil {
		t.Fatalf("resolveBunAppEnvPath() error = %v", err)
	}
	appBinDir := filepath.Join(appEnvPath, "node_modules", ".bin")
	wantPath := filepath.Dir(bunNodeAliasPath(appEnvPath)) + string(os.PathListSeparator) + appBinDir
	if info.Env["PATH"] != wantPath {
		t.Errorf("PATH = %q, want only the stable Bun alias and app dependency dirs %q", info.Env["PATH"], wantPath)
	}
	if info.Artifact != info.Args[5] {
		t.Errorf("Artifact = %q, want app entrypoint %q", info.Artifact, info.Args[5])
	}
	if inherited := os.Getenv("PATH"); inherited != "" && strings.Contains(info.Env["PATH"], inherited) {
		t.Errorf("PATH = %q captured the current process PATH", info.Env["PATH"])
	}
}

func TestInstallBunAppWithSystemRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX fake Bun executable")
	}

	storeRoot := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", storeRoot)
	t.Setenv("DATAMITSU_OFFLINE", "")
	pnpmPath := seedBunTestPNPM(t)
	fakeBun := filepath.Join(t.TempDir(), "bun")
	script := `#!/bin/sh
set -eu
mkdir -p node_modules/demo
printf '{}\n' > node_modules/demo/package.json
printf 'console.log("ok")\n' > node_modules/demo/cli.js
printf 'lockfileVersion: 9.0\n' > pnpm-lock.yaml
printf '%s\n' "$@" > bun-args.txt
`
	if err := os.WriteFile(fakeBun, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake Bun: %v", err)
	}

	rm := New(bunSystemRuntime(fakeBun))
	app := &binmanager.AppConfigBun{
		PackageName: "demo",
		Version:     "1.0.0",
		BinPath:     "node_modules/demo/cli.js",
	}
	if err := rm.InstallBunApp(context.Background(), "demo", app, nil, nil, nil); err != nil {
		t.Fatalf("InstallBunApp() error = %v", err)
	}

	appPath, err := rm.resolveBunAppEnvPath("demo", app, nil, nil)
	if err != nil {
		t.Fatalf("resolveBunAppEnvPath() error = %v", err)
	}
	for _, name := range []string{"package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "node_modules/demo/package.json", "node_modules/demo/cli.js"} {
		if _, err := os.Stat(filepath.Join(appPath, name)); err != nil {
			t.Errorf("installed file %s: %v", name, err)
		}
	}
	if _, err := os.Stat(bunNodeAliasPath(appPath)); err != nil {
		t.Errorf("Bun node alias was not installed: %v", err)
	}
	args, err := os.ReadFile(filepath.Join(appPath, "bun-args.txt"))
	if err != nil {
		t.Fatalf("read fake Bun args: %v", err)
	}
	for _, want := range []string{"--config=" + os.DevNull, "--no-env-file", "run", "--bun", "--no-install", pnpmPath, "install", "--reporter=ndjson"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("install args %q do not contain %q", args, want)
		}
	}
	if strings.Contains(string(args), "--frozen-lockfile") {
		t.Errorf("lockfile generation install unexpectedly used --frozen-lockfile: %q", args)
	}
}

func TestInstallBunAppUsesFrozenLockfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX fake Bun executable")
	}

	storeRoot := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", storeRoot)
	t.Setenv("DATAMITSU_OFFLINE", "")
	seedBunTestPNPM(t)
	fakeBun := filepath.Join(t.TempDir(), "bun")
	script := `#!/bin/sh
set -eu
mkdir -p node_modules/demo
printf '{}\n' > node_modules/demo/package.json
printf 'console.log("ok")\n' > node_modules/demo/cli.js
printf '%s\n' "$@" > bun-args.txt
`
	if err := os.WriteFile(fakeBun, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake Bun: %v", err)
	}

	lock, err := CompressLockFile("lockfileVersion: '9.0'\n")
	if err != nil {
		t.Fatalf("CompressLockFile() error = %v", err)
	}
	rm := New(bunSystemRuntime(fakeBun))
	app := &binmanager.AppConfigBun{
		PackageName: "demo",
		Version:     "1.0.0",
		BinPath:     "node_modules/demo/cli.js",
		LockFile:    lock,
	}
	if err := rm.InstallBunApp(context.Background(), "demo", app, nil, nil, nil); err != nil {
		t.Fatalf("InstallBunApp() error = %v", err)
	}
	appPath, err := rm.resolveBunAppEnvPath("demo", app, nil, nil)
	if err != nil {
		t.Fatalf("resolveBunAppEnvPath() error = %v", err)
	}
	args, err := os.ReadFile(filepath.Join(appPath, "bun-args.txt"))
	if err != nil {
		t.Fatalf("read fake Bun args: %v", err)
	}
	if !strings.Contains(string(args), "--frozen-lockfile") {
		t.Errorf("install args %q do not contain --frozen-lockfile", args)
	}
	lockBytes, err := os.ReadFile(filepath.Join(appPath, "pnpm-lock.yaml"))
	if err != nil {
		t.Fatalf("read pnpm-lock.yaml: %v", err)
	}
	if string(lockBytes) != "lockfileVersion: '9.0'\n" {
		t.Errorf("pnpm-lock.yaml = %q", lockBytes)
	}
}

func TestGetCommandInfoBunMergesWorkspaceOnceOnCacheHit(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	rm := New(bunSystemRuntime("bun"))
	appConfig := &binmanager.AppConfigBun{
		PackageName: "eslint",
		Version:     "10.9.0",
		BinPath:     "node_modules/eslint/bin/eslint.js",
		Runtime:     "bun",
	}
	app := binmanager.App{Bun: appConfig}

	appEnvPath, err := rm.resolveBunAppEnvPath("eslint", appConfig, nil, nil)
	if err != nil {
		t.Fatalf("resolveBunAppEnvPath() error = %v", err)
	}
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)
	if err := os.MkdirAll(filepath.Dir(appBinPath), 0o755); err != nil {
		t.Fatalf("mkdir app entrypoint dir: %v", err)
	}
	if err := os.WriteFile(appBinPath, []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatalf("write app entrypoint: %v", err)
	}
	modulePackageJSON := filepath.Join(appEnvPath, "node_modules", appConfig.PackageName, "package.json")
	if err := os.WriteFile(modulePackageJSON, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write module package.json: %v", err)
	}
	if err := writeBunNodeAlias(appEnvPath, "bun"); err != nil {
		t.Fatalf("write Bun node alias: %v", err)
	}

	var merges atomic.Int32
	original := buildPNPMWorkspace
	buildPNPMWorkspace = func(files map[string]string) (string, error) {
		merges.Add(1)
		return original(files)
	}
	defer func() { buildPNPMWorkspace = original }()

	if _, err := rm.GetCommandInfo(context.Background(), "eslint", app); err != nil {
		t.Fatalf("GetCommandInfo() error = %v", err)
	}
	if got := merges.Load(); got != 1 {
		t.Errorf("pnpm-workspace.yaml merge ran %d times per GetCommandInfo, want 1", got)
	}
}

func TestInstallBunAppRejectsInvalidWorkspaceYAML(t *testing.T) {
	rm := New(bunSystemRuntime("bun"))
	appConfig := &binmanager.AppConfigBun{
		PackageName: "eslint",
		Version:     "10.9.0",
		BinPath:     "node_modules/eslint/bin/eslint.js",
		Runtime:     "bun",
	}

	err := rm.InstallBunApp(context.Background(), "eslint", appConfig, nil, invalidWorkspaceFiles(), nil)
	if err == nil || !strings.Contains(err.Error(), "failed to compute pnpm-workspace.yaml") {
		t.Fatalf("InstallBunApp() error = %v, want workspace parse error", err)
	}
}
