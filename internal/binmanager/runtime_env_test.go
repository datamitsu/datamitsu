package binmanager

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestRuntimeEnvCommandResolution(t *testing.T) {
	for _, kind := range []string{"binary", "shell", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			cache := t.TempDir()
			t.Setenv("DATAMITSU_CACHE_DIR", cache)
			t.Setenv("DATAMITSU_OFFLINE", "1")
			apps := binaryAppFixture(t, "dep")
			root := apps["dep"]
			root.DependsOn = []string{"dep"}
			root.Env = map[string]string{"COMMON": "${STORE}/common"}
			root.RuntimeEnv = map[string]string{"EXACT": "${APP_BIN:dep}", "PATHS": "${STORE}:${APP_DIR}", "RESERVED": "user"}
			if kind == "shell" {
				root.Binary = nil
				root.Shell = &AppConfigShell{Name: "echo"}
			}
			appDir := filepath.Join(cache, "runtime")
			installCalls := 0
			mock := &mockRuntimeAppManager{
				computeAppPathFunc: func(string, App) (string, error) { return appDir, nil },
				resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) {
					return &CommandInfo{Command: filepath.Join(appDir, "tool"), Env: map[string]string{"RESERVED": "runtime"}}, nil
				},
				getCommandInfoFunc: func(context.Context, string, App) (*CommandInfo, error) {
					installCalls++
					return &CommandInfo{Command: filepath.Join(appDir, "tool"), Env: map[string]string{"RESERVED": "runtime"}}, nil
				},
			}
			if kind == "runtime" {
				root.Binary = nil
				root.Uv = &AppConfigUV{}
			}
			apps["root"] = root
			bm := New(apps, nil, mock)
			depPath, err := bm.getBinaryPath("dep")
			if err != nil {
				t.Fatal(err)
			}
			before := NetworkDownloads()
			info, installed, err := bm.ResolveCommandInfo("root")
			if err != nil {
				t.Fatal(err)
			}
			if installed {
				t.Fatal("missing dependency reported installed")
			}
			entries, err := os.ReadDir(cache)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 || NetworkDownloads() != before || installCalls != 0 {
				t.Fatal("read-only resolution changed store or downloaded")
			}
			dir, _ := bm.ComputeInstallPath("root")
			depDir := mustDependencyPathDir(t, dependencyLink{name: commandName("dep"), target: depPath})
			// Read-only resolution records the dependency directory as a PATH prefix only; the
			// source-mode shim prepends it to the caller's PATH.
			want := map[string]string{"EXACT": depPath, "PATHS": env.GetStorePath() + ":" + dir, "COMMON": env.GetStorePath() + "/common", "RESERVED": "user", "PATH": depDir}
			if kind == "runtime" {
				want["RESERVED"] = "runtime"
			}
			if !maps.Equal(info.Env, want) {
				t.Fatalf("env = %v, want %v", info.Env, want)
			}
			if !slices.Contains(info.RequiredPaths, depPath) {
				t.Fatalf("health = %v", info.RequiredPaths)
			}
			paths := []string{depPath}
			if kind != "shell" {
				paths = append(paths, info.Command)
			}
			for _, path := range paths {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			got, err := bm.GetCommandInfo(context.Background(), "root")
			if err != nil {
				t.Fatal(err)
			}
			want["PATH"] = depDir + string(os.PathListSeparator) + os.Getenv("PATH")
			if !maps.Equal(got.Env, want) {
				t.Fatalf("execution env = %v, want %v", got.Env, want)
			}
			if err := os.Remove(depPath); err != nil {
				t.Fatal(err)
			}
			if _, present, err := bm.ResolveCommandInfo("root"); err != nil || present {
				t.Fatalf("deleted dependency: present=%v, err=%v", present, err)
			}
		})
	}
}

func TestRuntimeEnvResolutionErrors(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		deps        []string
		want        string
	}{
		{"unknown", "${APP_BIN:missing}", []string{"missing"}, "not found"},
		{"no edge", "${APP_BIN:dep}", nil, "direct dependsOn"},
		{"unsupported", "${APP_BIN:unsupported}", []string{"unsupported"}, "not available for"},
		{"malformed", "${APP_BIN:}", nil, "malformed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			apps := binaryAppFixture(t, "dep")
			apps["unsupported"] = App{Binary: &AppConfigBinary{}}
			app := App{Shell: &AppConfigShell{Name: "echo"}, DependsOn: tt.deps, RuntimeEnv: map[string]string{"BIN": tt.value}}
			apps["root"] = app
			bm := New(apps, nil, nil)
			if _, err := bm.getCommandInfo(context.Background(), "root"); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestAppBinaryBindingStrictness(t *testing.T) {
	for _, tt := range []struct {
		name, value, path string
		err               error
		want              string
	}{
		{name: "empty resolution", value: "${APP_BIN:dep}", want: "empty path"},
		{name: "resolution failure", value: "${APP_BIN:dep}", err: errors.New("missing target"), want: "missing target"},
		{name: "unterminated", value: "${APP_BIN:dep", want: "unterminated"},
		{name: "unknown form", value: "${FOO:dep}", want: "unknown placeholder"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ExpandAppBinaryBindings(tt.value, func(string) (string, error) { return tt.path, tt.err })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRuntimeEnvPreservesBinaryIdentity(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps := binaryAppFixture(t, "tool")
	bm := New(apps, nil, nil)
	before, err := bm.getBinaryPath("tool")
	if err != nil {
		t.Fatal(err)
	}
	app := apps["tool"]
	app.RuntimeEnv = map[string]string{"BIN": "${APP_BIN:dep}"}
	apps["tool"] = app
	after, err := bm.getBinaryPath("tool")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("runtimeEnv changed install identity: %s -> %s", before, after)
	}
}

func TestResolveDependencyRuntimeHealth(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	dir := t.TempDir()
	apps := MapOfApps{
		"root":   {Shell: &AppConfigShell{Name: "echo"}, DependsOn: []string{"middle"}},
		"middle": {Uv: &AppConfigUV{}, DependsOn: []string{"leaf"}},
		"leaf":   {Node: &AppConfigNode{}},
	}
	mock := &mockRuntimeAppManager{resolveCommandInfoFunc: func(name string, _ App) (*CommandInfo, error) {
		return &CommandInfo{Command: filepath.Join(dir, name), RequiredPaths: []string{filepath.Join(dir, name+"-runtime")}}, nil
	}, getCommandInfoFunc: func(context.Context, string, App) (*CommandInfo, error) {
		t.Fatal("read-only resolution called installer")
		return nil, errors.New("unexpected installation")
	}}
	bm := New(apps, nil, mock)
	want := []string{filepath.Join(dir, "leaf"), filepath.Join(dir, "leaf-runtime"), filepath.Join(dir, "middle"), filepath.Join(dir, "middle-runtime")}
	for _, present := range []bool{false, true} {
		if present {
			for _, path := range want {
				if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
		info, installed, err := bm.ResolveCommandInfo("root")
		if err != nil {
			t.Fatal(err)
		}
		if installed != present || !slices.Equal(info.RequiredPaths, want) {
			t.Fatalf("installed=%v, health=%v", installed, info.RequiredPaths)
		}
	}
}

func TestRuntimeEnvPreservesLiteralPaths(t *testing.T) {
	for _, segment := range []string{"${STORE}", "${APP_DIR}", "${APP_BIN:missing}", "${FOO:value}", "${"} {
		t.Run(segment, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(t.TempDir(), segment))
			t.Setenv("DATAMITSU_OFFLINE", "1")
			apps := binaryAppFixture(t, "dep")
			root := apps["dep"]
			root.DependsOn = []string{"dep"}
			root.RuntimeEnv = map[string]string{"BINDING": "${APP_BIN:dep}", "DIR": "${APP_DIR}", "STORE": "${STORE}", "MIXED": "${APP_BIN:dep}|${STORE}|${APP_DIR}|${APP_BIN:dep}"}
			apps["root"] = root
			bm := New(apps, nil, nil)
			depPath, err := bm.getBinaryPath("dep")
			if err != nil {
				t.Fatal(err)
			}
			rootPath, err := bm.ComputeInstallPath("root")
			if err != nil {
				t.Fatal(err)
			}
			info, _, err := bm.ResolveCommandInfo("root")
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{
				"BINDING": depPath, "DIR": rootPath, "STORE": env.GetStorePath(),
				"MIXED": strings.Join([]string{depPath, env.GetStorePath(), rootPath, depPath}, "|"),
				"PATH":  mustDependencyPathDir(t, dependencyLink{name: commandName("dep"), target: depPath}),
			}
			if !maps.Equal(info.Env, want) {
				t.Fatalf("env = %v, want %v", info.Env, want)
			}
		})
	}
}
