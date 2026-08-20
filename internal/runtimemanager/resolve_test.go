package runtimemanager

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// plantRuntimeBinary creates the file a managed runtime resolves to, so the
// install-carrying command-info path finds a cache hit and never downloads.
// This is what lets a test compare GetCommandInfo against ResolveCommandInfo
// offline: the two must agree byte-for-byte on Command, Args and Env.
func plantRuntimeBinary(t *testing.T, rm *RuntimeManager, runtimeName string) string {
	t.Helper()
	binPath, err := rm.ResolveRuntimePath(runtimeName)
	if err != nil {
		t.Fatalf("ResolveRuntimePath(%q) error = %v", runtimeName, err)
	}
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return binPath
}

func jvmRuntimes(mode config.RuntimeMode) config.MapOfRuntimes {
	rc := config.RuntimeConfig{Kind: config.RuntimeKindJVM, Mode: mode}
	if mode == config.RuntimeModeSystem {
		rc.System = &config.RuntimeConfigSystem{Command: "java"}
	}
	return config.MapOfRuntimes{"jvm": rc}
}

// TestResolveRuntimePath_NoInstall asserts the pure path lookup answers for a
// runtime that has never been downloaded, and does so without entering a
// download path. This is the primitive both resolveNodeBinPath and the JVM
// resolver are built on.
func TestResolveRuntimePath_NoInstall(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	rm := New(nodeRuntimeWith(t, "http://127.0.0.1:1/node.tar.xz", "abc", testLibc))

	beforeRM, beforeBM := DownloaderConstructions(), binmanager.NetworkDownloads()
	binPath, err := rm.ResolveRuntimePath("node")
	if err != nil {
		t.Fatalf("ResolveRuntimePath() error = %v", err)
	}
	if binPath == "" {
		t.Fatal("ResolveRuntimePath returned an empty path")
	}
	if _, statErr := os.Stat(binPath); statErr == nil {
		t.Errorf("resolve materialized %q; it must not download", binPath)
	}
	assertNoDownloads(t, beforeRM, beforeBM)
}

// TestResolveRuntimePath_SystemMode asserts a system-mode runtime resolves to
// the configured command with no store path involved.
func TestResolveRuntimePath_SystemMode(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	rm := New(jvmRuntimes(config.RuntimeModeSystem))

	got, err := rm.ResolveRuntimePath("jvm")
	if err != nil {
		t.Fatalf("ResolveRuntimePath() error = %v", err)
	}
	if got != "java" {
		t.Errorf("ResolveRuntimePath() = %q, want %q", got, "java")
	}
}

// TestResolveRuntimePath_Errors covers the error branches shared with
// getRuntimePath: an unregistered runtime and a mode with no matching config.
func TestResolveRuntimePath_Errors(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	tests := []struct {
		name     string
		runtimes config.MapOfRuntimes
		lookup   string
	}{
		{
			name:     "unknown runtime",
			runtimes: config.MapOfRuntimes{},
			lookup:   "node",
		},
		{
			name:     "system mode without system config",
			runtimes: config.MapOfRuntimes{"jvm": {Kind: config.RuntimeKindJVM, Mode: config.RuntimeModeSystem}},
			lookup:   "jvm",
		},
		{
			name:     "managed mode without managed config",
			runtimes: config.MapOfRuntimes{"jvm": {Kind: config.RuntimeKindJVM, Mode: config.RuntimeModeManaged}},
			lookup:   "jvm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rm := New(tt.runtimes)
			if _, err := rm.ResolveRuntimePath(tt.lookup); err == nil {
				t.Error("ResolveRuntimePath() error = nil, want an error")
			}
		})
	}
}

// TestResolveCommandInfo_NodeMatchesGetCommandInfo is the parity test that
// keeps the two node paths honest: the side-effect-free resolver must produce
// the same Command, Args and env — the managed node bin dir on PATH and the
// npm_config_* triple — that the exec path produces. A drift here means a shim
// would run a node app under a different environment than `datamitsu exec` does.
//
// PATH is the one deliberate difference and is asserted separately below. The
// exec path composes and uses it immediately, so it folds in the current
// process's PATH; the resolver's answer is persisted in the source-mode farm
// manifest and replayed by later shells, so it records only the runtime-owned
// directory and the shim prepends it to the caller's live PATH.
func TestResolveCommandInfo_NodeMatchesGetCommandInfo(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	rm := New(nodeRuntimeWith(t, "http://127.0.0.1:1/node.tar.xz", "abc", testLibc))
	nodeBin := plantRuntimeBinary(t, rm, "node")

	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}
	app := binmanager.App{Node: appConfig}

	beforeRM, beforeBM := DownloaderConstructions(), binmanager.NetworkDownloads()
	resolved, err := rm.ResolveCommandInfo("eslint", app)
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	assertNoDownloads(t, beforeRM, beforeBM)

	want, err := rm.GetNodeCommandInfo(context.Background(), "eslint", appConfig, nil, nil)
	if err != nil {
		t.Fatalf("GetNodeCommandInfo() error = %v", err)
	}

	if resolved.Type != want.Type {
		t.Errorf("Type = %q, want %q", resolved.Type, want.Type)
	}
	if resolved.Command != want.Command {
		t.Errorf("Command = %q, want %q", resolved.Command, want.Command)
	}
	if !slices.Equal(resolved.Args, want.Args) {
		t.Errorf("Args = %v, want %v", resolved.Args, want.Args)
	}
	resolvedRest, wantRest := withoutPath(resolved.Env), withoutPath(want.Env)
	if !maps.Equal(resolvedRest, wantRest) {
		t.Errorf("Env (excluding PATH) = %v, want %v", resolvedRest, wantRest)
	}

	// Pin the two properties the shim depends on explicitly, so a change that
	// broke both paths in the same way would still fail.
	nodeBinDir := filepath.Dir(nodeBin)
	if resolved.Env["PATH"] != nodeBinDir {
		t.Errorf("Env[PATH] = %q, want exactly the managed node bin dir %q", resolved.Env["PATH"], nodeBinDir)
	}
	// Nothing of the baking process's own PATH may be recorded: the manifest
	// outlives the shell that wrote it, and a per-shell version manager's
	// directory is gone by the time another shell replays it.
	if inherited := os.Getenv("PATH"); inherited != "" && strings.Contains(resolved.Env["PATH"], inherited) {
		t.Errorf("Env[PATH] = %q captured the current process's PATH", resolved.Env["PATH"])
	}
	// The exec path, by contrast, does compose the live PATH, because it is
	// about to use it.
	if !strings.HasPrefix(want.Env["PATH"], nodeBinDir+string(os.PathListSeparator)) {
		t.Errorf("exec-path Env[PATH] = %q, want it to start with %q", want.Env["PATH"], nodeBinDir)
	}
	for _, key := range []string{"npm_config_store_dir", "npm_config_virtual_store_dir", "npm_config_global_dir"} {
		if resolved.Env[key] == "" {
			t.Errorf("Env[%s] is empty, want the node app env to carry it", key)
		}
	}
}

// withoutPath copies env minus PATH, which the two node paths intentionally
// disagree on.
func withoutPath(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		if k == "PATH" {
			continue
		}
		out[k] = v
	}
	return out
}

// TestResolveCommandInfo_NodeUninstalledRuntime asserts the resolver answers
// for a node app whose runtime has not been downloaded at all — the case
// source mode hits on a fresh clone.
func TestResolveCommandInfo_NodeUninstalledRuntime(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	rm := New(nodeRuntimeWith(t, "http://127.0.0.1:1/node.tar.xz", "abc", testLibc))
	app := binmanager.App{Node: &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}}

	beforeRM, beforeBM := DownloaderConstructions(), binmanager.NetworkDownloads()
	info, err := rm.ResolveCommandInfo("eslint", app)
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	assertNoDownloads(t, beforeRM, beforeBM)

	if !strings.HasSuffix(info.Command, filepath.FromSlash("node_modules/.bin/eslint")) {
		t.Errorf("Command = %q, want it to end with the app's binPath", info.Command)
	}
	if _, statErr := os.Stat(info.Command); statErr == nil {
		t.Errorf("resolve materialized %q; it must not install", info.Command)
	}
}

// TestResolveCommandInfo_JVM asserts a jvm app resolves to the java binary with
// the jar in Args — argv a symlink cannot carry, which is why jvm apps are
// always shim entries in the farm.
func TestResolveCommandInfo_JVM(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	rm := New(jvmRuntimes(config.RuntimeModeSystem))
	app := binmanager.App{Jvm: &binmanager.AppConfigJVM{
		Version: "6.11.0",
		JarURL:  "https://example.com/spectral.jar",
		JarHash: "0000000000000000000000000000000000000000000000000000000000000000",
		Runtime: "jvm",
	}}

	beforeRM, beforeBM := DownloaderConstructions(), binmanager.NetworkDownloads()
	info, err := rm.ResolveCommandInfo("spectral", app)
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	assertNoDownloads(t, beforeRM, beforeBM)

	if info.Type != "jvm" {
		t.Errorf("Type = %q, want %q", info.Type, "jvm")
	}
	if info.Command != "java" {
		t.Errorf("Command = %q, want %q", info.Command, "java")
	}
	if len(info.Args) != 2 || info.Args[0] != "-jar" {
		t.Fatalf("Args = %v, want [-jar <jar>]", info.Args)
	}
	if !strings.HasSuffix(info.Args[1], "spectral.jar") {
		t.Errorf("Args[1] = %q, want it to end with spectral.jar", info.Args[1])
	}
}

// TestResolveCommandInfo_JVMMainClass covers the -cp/mainClass variant, so the
// resolver is not silently pinned to the -jar shape.
func TestResolveCommandInfo_JVMMainClass(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	rm := New(jvmRuntimes(config.RuntimeModeSystem))
	app := binmanager.App{Jvm: &binmanager.AppConfigJVM{
		Version:   "1.0.0",
		JarURL:    "https://example.com/tool.jar",
		JarHash:   "0000000000000000000000000000000000000000000000000000000000000000",
		MainClass: "com.example.Main",
		Runtime:   "jvm",
	}}

	info, err := rm.ResolveCommandInfo("tool", app)
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	if len(info.Args) != 3 || info.Args[0] != "-cp" || info.Args[2] != "com.example.Main" {
		t.Errorf("Args = %v, want [-cp <jar> com.example.Main]", info.Args)
	}
}

// TestResolveCommandInfo_JVMManagedUninstalled asserts a managed JVM that has
// never been downloaded resolves to the store path it will occupy rather than
// triggering a fetch.
func TestResolveCommandInfo_JVMManagedUninstalled(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	runtimes := nodeRuntimeWith(t, "http://127.0.0.1:1/jdk.tar.xz", "abc", testLibc)
	jvm := runtimes["node"]
	jvm.Kind = config.RuntimeKindJVM
	jvm.Node = nil
	runtimes = config.MapOfRuntimes{"jvm": jvm}

	rm := New(runtimes)
	app := binmanager.App{Jvm: &binmanager.AppConfigJVM{
		Version: "1.0.0",
		JarURL:  "https://example.com/tool.jar",
		JarHash: "0000000000000000000000000000000000000000000000000000000000000000",
		Runtime: "jvm",
	}}

	beforeRM, beforeBM := DownloaderConstructions(), binmanager.NetworkDownloads()
	info, err := rm.ResolveCommandInfo("tool", app)
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	assertNoDownloads(t, beforeRM, beforeBM)

	if _, statErr := os.Stat(info.Command); statErr == nil {
		t.Errorf("resolve materialized the java binary at %q; it must not download", info.Command)
	}
}

// TestResolveCommandInfo_RequiredPaths pins what each kind reports as required
// beyond the path that gets exec'd, which is what the source-mode farm records
// and the shim replays before deciding an install can be skipped.
//
// A node app needs its installed package and — for a managed runtime — the node
// binary its .bin shim resolves through `#!/usr/bin/env node`; without the
// latter the lookup falls through PATH to whatever node the system has. A
// managed JVM app needs its java. A system-mode runtime's interpreter is the
// user's to supply and is deliberately absent: reinstalling would not produce
// it.
func TestResolveCommandInfo_RequiredPaths(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	t.Run("node names its package and its managed runtime", func(t *testing.T) {
		rm := New(nodeRuntimeWith(t, "http://127.0.0.1:1/node.tar.xz", "abc", testLibc))
		nodeBin := plantRuntimeBinary(t, rm, "node")
		app := binmanager.App{Node: &binmanager.AppConfigNode{
			PackageName: "eslint",
			Version:     "9.0.0",
			BinPath:     "node_modules/.bin/eslint",
			Runtime:     "node",
		}}

		info, err := rm.ResolveCommandInfo("eslint", app)
		if err != nil {
			t.Fatalf("ResolveCommandInfo() error = %v", err)
		}
		if !slices.Contains(info.RequiredPaths, nodeBin) {
			t.Errorf("RequiredPaths = %v, want it to contain the managed node %q", info.RequiredPaths, nodeBin)
		}
		wantModule := filepath.Join("node_modules", "eslint", "package.json")
		if !slices.ContainsFunc(info.RequiredPaths, func(p string) bool { return strings.HasSuffix(p, wantModule) }) {
			t.Errorf("RequiredPaths = %v, want one ending in %q", info.RequiredPaths, wantModule)
		}
	})

	t.Run("managed jvm names its java", func(t *testing.T) {
		runtimes := nodeRuntimeWith(t, "http://127.0.0.1:1/jdk.tar.xz", "abc", testLibc)
		jvm := runtimes["node"]
		jvm.Kind = config.RuntimeKindJVM
		jvm.Node = nil
		rm := New(config.MapOfRuntimes{"jvm": jvm})
		app := binmanager.App{Jvm: &binmanager.AppConfigJVM{
			Version: "1.0.0",
			JarURL:  "https://example.com/tool.jar",
			JarHash: "0000000000000000000000000000000000000000000000000000000000000000",
			Runtime: "jvm",
		}}

		info, err := rm.ResolveCommandInfo("tool", app)
		if err != nil {
			t.Fatalf("ResolveCommandInfo() error = %v", err)
		}
		if !slices.Equal(info.RequiredPaths, []string{info.Command}) {
			t.Errorf("RequiredPaths = %v, want [%s]", info.RequiredPaths, info.Command)
		}
	})

	t.Run("system jvm requires nothing extra", func(t *testing.T) {
		rm := New(jvmRuntimes(config.RuntimeModeSystem))
		app := binmanager.App{Jvm: &binmanager.AppConfigJVM{
			Version: "6.11.0",
			JarURL:  "https://example.com/spectral.jar",
			JarHash: "0000000000000000000000000000000000000000000000000000000000000000",
			Runtime: "jvm",
		}}

		info, err := rm.ResolveCommandInfo("spectral", app)
		if err != nil {
			t.Fatalf("ResolveCommandInfo() error = %v", err)
		}
		if len(info.RequiredPaths) != 0 {
			t.Errorf("RequiredPaths = %v, want none for a system-mode java the user supplies", info.RequiredPaths)
		}
	})
}

// TestResolveCommandInfo_UVAndGo asserts the two already-install-free kinds are
// wired into the resolver and report the paths their apps will occupy.
func TestResolveCommandInfo_UVAndGo(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	rm := New(config.MapOfRuntimes{
		"uv": {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "uv"}},
		"go": {Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "go"}},
	})

	tests := []struct {
		name     string
		app      binmanager.App
		wantType string
		wantSfx  string
	}{
		{
			name:     "uv",
			app:      binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.38.0", Runtime: "uv"}},
			wantType: "uv",
			wantSfx:  "yamllint",
		},
		{
			name:     "go",
			app:      binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "gofumpt", Version: "0.9.1", Runtime: "go"}},
			wantType: "go",
			wantSfx:  "gofumpt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeRM, beforeBM := DownloaderConstructions(), binmanager.NetworkDownloads()
			info, err := rm.ResolveCommandInfo(tt.wantSfx, tt.app)
			if err != nil {
				t.Fatalf("ResolveCommandInfo() error = %v", err)
			}
			assertNoDownloads(t, beforeRM, beforeBM)
			if info.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", info.Type, tt.wantType)
			}
			if !strings.HasSuffix(info.Command, tt.wantSfx) {
				t.Errorf("Command = %q, want it to end with %q", info.Command, tt.wantSfx)
			}
		})
	}
}

// TestResolveCommandInfo_NotRuntimeManaged pins the error shape to
// GetCommandInfo's for an app that is not runtime-managed.
func TestResolveCommandInfo_NotRuntimeManaged(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	rm := New(config.MapOfRuntimes{})
	app := binmanager.App{Shell: &binmanager.AppConfigShell{Name: "echo"}}

	_, resolveErr := rm.ResolveCommandInfo("echo", app)
	if resolveErr == nil {
		t.Fatal("ResolveCommandInfo() error = nil, want an error for a non-runtime app")
	}
	_, getErr := rm.GetCommandInfo(context.Background(), "echo", app)
	if getErr == nil {
		t.Fatal("GetCommandInfo() error = nil, want an error for a non-runtime app")
	}
	if resolveErr.Error() != getErr.Error() {
		t.Errorf("error shapes differ:\n  resolve: %v\n  get:     %v", resolveErr, getErr)
	}
}

// assertNoDownloads is the guard that keeps the resolution path pure: both
// packages count every point at which they build something that fetches from
// the network, and resolution must leave both counters untouched. A refactor
// that reintroduces a download fails here rather than merely getting slower —
// or, offline, failing for a misleading reason.
func assertNoDownloads(t *testing.T, beforeRM, beforeBM int64) {
	t.Helper()
	if got := DownloaderConstructions() - beforeRM; got != 0 {
		t.Errorf("resolution constructed %d runtimemanager download paths, want 0", got)
	}
	if got := binmanager.NetworkDownloads() - beforeBM; got != 0 {
		t.Errorf("resolution started %d binmanager downloads, want 0", got)
	}
}

// TestResolveCommandInfo_NodeSystemBareCommand pins that a system-mode node
// runtime naming its interpreter by bare word contributes no PATH entry at all.
//
// filepath.Dir("node") is ".", and recording that would put the current working
// directory in front of every lookup the app makes — persisted into the farm
// manifest, so every shell that ever replays the entry inherits it. A bare word
// is found through PATH by the exec itself, so there is nothing to contribute.
func TestResolveCommandInfo_NodeSystemBareCommand(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	rm := New(config.MapOfRuntimes{
		"node": {
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeSystem,
			Node: &config.RuntimeConfigNode{
				NodeVersion: "26.2.0",
				PNPMVersion: "11.0.0",
				PNPMHash:    "0000000000000000000000000000000000000000000000000000000000000000",
			},
			System: &config.RuntimeConfigSystem{Command: "node"},
		},
	})

	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}

	resolved, err := rm.ResolveCommandInfo("eslint", binmanager.App{Node: appConfig})
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	if path, ok := resolved.Env["PATH"]; ok {
		t.Errorf("Env[PATH] = %q, want no PATH entry at all for a bare system command", path)
	}

	// The exec path composes a live PATH and must not lead it with "." either.
	execInfo, err := rm.GetNodeCommandInfo(context.Background(), "eslint", appConfig, nil, nil)
	if err != nil {
		t.Fatalf("GetNodeCommandInfo() error = %v", err)
	}
	if strings.HasPrefix(execInfo.Env["PATH"], "."+string(os.PathListSeparator)) {
		t.Errorf("exec-path Env[PATH] = %q, want the working directory not to lead it", execInfo.Env["PATH"])
	}
}
