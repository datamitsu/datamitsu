package binmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/datamitsu/datamitsu/internal/syslist"
)

// binaryAppFixture builds a one-app registry whose single binary points at an
// unreachable URL: any attempt to download it fails loudly, so a resolve that
// silently reintroduced a fetch could not pass as a success.
func binaryAppFixture(t *testing.T, name string) MapOfApps {
	t.Helper()
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("detect os type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("detect arch type: %v", err)
	}
	return MapOfApps{
		name: App{
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					osType: {
						archType: {
							"unknown": BinaryOsArchInfo{
								URL:         "http://127.0.0.1:1/never-reachable",
								Hash:        "0000000000000000000000000000000000000000000000000000000000000000",
								ContentType: BinContentTypeBinary,
							},
							"glibc": BinaryOsArchInfo{
								URL:         "http://127.0.0.1:1/never-reachable",
								Hash:        "0000000000000000000000000000000000000000000000000000000000000000",
								ContentType: BinContentTypeBinary,
							},
							"musl": BinaryOsArchInfo{
								URL:         "http://127.0.0.1:1/never-reachable",
								Hash:        "0000000000000000000000000000000000000000000000000000000000000000",
								ContentType: BinContentTypeBinary,
							},
						},
					},
				},
			},
		},
	}
}

// TestResolveCommandInfo_UninstalledBinary is the property source mode depends
// on: asking where an app lives must be answerable for an app that has never
// been downloaded, without downloading it. Offline mode plus the network-
// download counter make a reintroduced fetch a test failure rather than a
// silent slowdown.
func TestResolveCommandInfo_UninstalledBinary(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	bm := New(binaryAppFixture(t, "tofu"), nil, nil)

	before := NetworkDownloads()
	info, installed, err := bm.ResolveCommandInfo("tofu")
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	if installed {
		t.Error("installed = true for an app with no store entry, want false")
	}
	if info.Type != "binary" {
		t.Errorf("Type = %q, want %q", info.Type, "binary")
	}
	if info.Command == "" {
		t.Error("Command is empty; resolution must report the path the app will occupy")
	}
	if _, statErr := os.Stat(info.Command); statErr == nil {
		t.Errorf("resolve materialized %q; it must not touch the store", info.Command)
	}
	if got := NetworkDownloads() - before; got != 0 {
		t.Errorf("resolve started %d downloads, want 0", got)
	}
}

// TestResolveCommandInfo_InstalledBinary asserts installed flips to true once
// the store entry exists, and that the reported path is the same one
// GetBinaryPath would hand the exec path.
func TestResolveCommandInfo_InstalledBinary(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")

	bm := New(binaryAppFixture(t, "tofu"), nil, nil)

	binPath, err := bm.getBinaryPath("tofu")
	if err != nil {
		t.Fatalf("getBinaryPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	before := NetworkDownloads()
	info, installed, err := bm.ResolveCommandInfo("tofu")
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	if !installed {
		t.Error("installed = false for an app present in the store, want true")
	}
	if info.Command != binPath {
		t.Errorf("Command = %q, want %q", info.Command, binPath)
	}
	if got := NetworkDownloads() - before; got != 0 {
		t.Errorf("resolve started %d downloads, want 0", got)
	}
}

// TestResolveCommandInfo_ShellApp asserts a shell app resolves to its bare
// command name and reports installed without consulting the filesystem: its
// executable is found through the inherited PATH at spawn time, so there is no
// store path to stat.
func TestResolveCommandInfo_ShellApp(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	bm := New(MapOfApps{
		"echo": App{Shell: &AppConfigShell{Name: "echo", Args: []string{"-n"}}},
	}, nil, nil)

	info, installed, err := bm.ResolveCommandInfo("echo")
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	if info.Type != "shell" {
		t.Errorf("Type = %q, want %q", info.Type, "shell")
	}
	if info.Command != "echo" {
		t.Errorf("Command = %q, want %q", info.Command, "echo")
	}
	if len(info.Args) != 1 || info.Args[0] != "-n" {
		t.Errorf("Args = %v, want [-n]", info.Args)
	}
	if !installed {
		t.Error("installed = false for a shell app, want true")
	}
}

// TestResolveCommandInfo_UnknownApp pins the error shape to GetCommandInfo's:
// callers switching between the two must not have to special-case wording.
func TestResolveCommandInfo_UnknownApp(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	bm := New(MapOfApps{}, nil, nil)

	_, _, resolveErr := bm.ResolveCommandInfo("nope")
	if resolveErr == nil {
		t.Fatal("ResolveCommandInfo() error = nil, want an error for an unknown app")
	}
	_, getErr := bm.GetCommandInfo(context.Background(), "nope")
	if getErr == nil {
		t.Fatal("GetCommandInfo() error = nil, want an error for an unknown app")
	}
	if resolveErr.Error() != getErr.Error() {
		t.Errorf("error shapes differ:\n  resolve: %v\n  get:     %v", resolveErr, getErr)
	}
}

// TestResolveCommandInfo_MergesAppEnv asserts the app's own Env is merged
// exactly as GetCommandInfo merges it, including ${APP_DIR} expansion, and
// that a reserved runtime key is never overridden by the user config.
func TestResolveCommandInfo_MergesAppEnv(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	mock := &mockRuntimeAppManager{
		resolveCommandInfoFunc: func(appName string, _ App) (*CommandInfo, error) {
			return &CommandInfo{
				Type:    "uv",
				Command: "/nonexistent/store/" + appName,
				Env:     map[string]string{"UV_CACHE_DIR": "/reserved"},
			}, nil
		},
		computeAppPathFunc: func(appName string, _ App) (string, error) {
			return "/apps/" + appName, nil
		},
	}

	bm := New(MapOfApps{
		"yamllint": App{
			Uv:  &AppConfigUV{PackageName: "yamllint", Version: "1.38.0"},
			Env: map[string]string{"CUSTOM": "${APP_DIR}/x", "UV_CACHE_DIR": "/hijack"},
		},
	}, nil, mock)

	info, installed, err := bm.ResolveCommandInfo("yamllint")
	if err != nil {
		t.Fatalf("ResolveCommandInfo() error = %v", err)
	}
	if installed {
		t.Error("installed = true for a nonexistent command path, want false")
	}
	if got, want := info.Env["CUSTOM"], "/apps/yamllint/x"; got != want {
		t.Errorf("Env[CUSTOM] = %q, want %q", got, want)
	}
	if got, want := info.Env["UV_CACHE_DIR"], "/reserved"; got != want {
		t.Errorf("Env[UV_CACHE_DIR] = %q, want %q; a user config must not override a reserved key", got, want)
	}
}

// TestResolveCommandInfo_NoRuntimeManager asserts a runtime-managed app without
// a configured runtime manager errors rather than reporting a bogus path.
func TestResolveCommandInfo_NoRuntimeManager(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	bm := New(MapOfApps{
		"yamllint": App{Uv: &AppConfigUV{PackageName: "yamllint", Version: "1.38.0"}},
	}, nil, nil)

	if _, _, err := bm.ResolveCommandInfo("yamllint"); err == nil {
		t.Error("ResolveCommandInfo() error = nil, want an error when no runtime manager is configured")
	}
}

// TestResolveCommandInfo_RuntimeResolverError asserts a resolver failure
// propagates rather than degrading to installed=false, which would make a
// broken config indistinguishable from a not-yet-downloaded tool.
func TestResolveCommandInfo_RuntimeResolverError(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	mock := &mockRuntimeAppManager{
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) {
			return nil, errors.New("boom")
		},
	}
	bm := New(MapOfApps{
		"yamllint": App{Uv: &AppConfigUV{PackageName: "yamllint", Version: "1.38.0"}},
	}, nil, mock)

	if _, _, err := bm.ResolveCommandInfo("yamllint"); err == nil {
		t.Error("ResolveCommandInfo() error = nil, want the resolver error to propagate")
	}
}

// TestResolveCommandInfo_NoConfiguration asserts an app with no kind at all is
// an error, matching GetCommandInfo.
func TestResolveCommandInfo_NoConfiguration(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	bm := New(MapOfApps{"empty": App{}}, nil, nil)

	_, _, resolveErr := bm.ResolveCommandInfo("empty")
	if resolveErr == nil {
		t.Fatal("ResolveCommandInfo() error = nil, want an error for an app with no configuration")
	}
	_, getErr := bm.GetCommandInfo(context.Background(), "empty")
	if getErr == nil {
		t.Fatal("GetCommandInfo() error = nil, want an error for an app with no configuration")
	}
	if resolveErr.Error() != getErr.Error() {
		t.Errorf("error shapes differ:\n  resolve: %v\n  get:     %v", resolveErr, getErr)
	}
}
