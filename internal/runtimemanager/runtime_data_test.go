package runtimemanager

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
)

func TestRuntimeDataPathUsesRuntimeNameAndIdentity(t *testing.T) {
	rm := New(bunSystemRuntime("bun"))
	name, rc, err := rm.ResolveRuntime("bun", config.RuntimeKindBun)
	if err != nil {
		t.Fatal(err)
	}
	osType, archType, libc, err := rm.hostRuntimePlatform()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := rm.runtimeIdentityHash(rc, osType, archType, libc)
	if err != nil {
		t.Fatal(err)
	}

	got, err := rm.runtimeDataPath(name, rc)
	if err != nil {
		t.Fatal(err)
	}
	want := env.GetRuntimeDataPath(name, identity)
	if got != want {
		t.Fatalf("runtimeDataPath() = %q, want %q", got, want)
	}

	changed := rc
	changed.System = &config.RuntimeConfigSystem{Command: "bun", SystemVersion: "different"}
	other, err := rm.runtimeDataPath(name, changed)
	if err != nil {
		t.Fatal(err)
	}
	if other == got {
		t.Fatalf("runtime data path did not change with runtime identity: %q", got)
	}
}

func TestPNPMStorePathIncludesConsumingAndPNPMRuntimeIdentity(t *testing.T) {
	rm := New(bunSystemRuntime("bun"))
	name, rc, err := rm.ResolveRuntime("bun", config.RuntimeKindBun)
	if err != nil {
		t.Fatal(err)
	}
	osType, archType, libc, err := rm.hostRuntimePlatform()
	if err != nil {
		t.Fatal(err)
	}
	runtimeIdentity, err := rm.runtimeIdentityHash(rc, osType, archType, libc)
	if err != nil {
		t.Fatal(err)
	}
	pnpmName, pnpmIdentity, err := rm.pnpmIdentityHash(rc, osType, archType, libc)
	if err != nil {
		t.Fatal(err)
	}

	got, err := rm.pnpmDataPath(name, rc)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(env.GetRuntimeDataPath(name, runtimeIdentity), "pnpm", pnpmName, pnpmIdentity, "store")
	if got != want {
		t.Fatalf("pnpmDataPath() = %q, want %q", got, want)
	}

	changedRuntimes := bunSystemRuntime("bun")
	pnpm := changedRuntimes[testPNPMRuntimeName]
	pnpm.PNPM = &config.RuntimeConfigPNPM{PNPMVersion: "12.4.2"}
	changedRuntimes[testPNPMRuntimeName] = pnpm
	changedManager := New(changedRuntimes)
	changedName, changedRC, err := changedManager.ResolveRuntime("bun", config.RuntimeKindBun)
	if err != nil {
		t.Fatal(err)
	}
	changedPath, err := changedManager.pnpmDataPath(changedName, changedRC)
	if err != nil {
		t.Fatal(err)
	}
	if changedPath == got {
		t.Fatalf("pnpm store path did not change with pnpm identity: %q", got)
	}
}

func TestRuntimeOwnedEnvironmentBuildsPerKindPaths(t *testing.T) {
	appEnvPath := "/store/.apps/tool/identity"
	runtimes := bunSystemRuntime("bun")
	runtimes["node"] = config.RuntimeConfig{
		Kind:   config.RuntimeKindNode,
		Mode:   config.RuntimeModeSystem,
		System: &config.RuntimeConfigSystem{Command: "node", SystemVersion: "22"},
		Node:   &config.RuntimeConfigNode{NodeVersion: "22", PNPMRuntime: testPNPMRuntimeName},
	}
	runtimes["uv"] = config.RuntimeConfig{
		Kind:   config.RuntimeKindUV,
		Mode:   config.RuntimeModeSystem,
		System: &config.RuntimeConfigSystem{Command: "uv", SystemVersion: "1"},
		UV:     &config.RuntimeConfigUV{PythonVersion: "3.12"},
	}
	runtimes["go"] = config.RuntimeConfig{
		Kind:   config.RuntimeKindGo,
		Mode:   config.RuntimeModeSystem,
		System: &config.RuntimeConfigSystem{Command: "go", SystemVersion: "1.23"},
		Go:     &config.RuntimeConfigGo{GoVersion: "1.23"},
	}
	rm := New(runtimes)

	for _, name := range []string{"node", "bun", "uv", "go"} {
		rc := runtimes[name]
		root, vars, err := rm.RuntimeOwnedEnvironment(name, rc, appEnvPath)
		if err != nil {
			t.Fatalf("RuntimeOwnedEnvironment(%q): %v", name, err)
		}
		osType, archType, libc, err := rm.hostRuntimePlatform()
		if err != nil {
			t.Fatal(err)
		}
		identity, err := rm.runtimeIdentityHash(rc, osType, archType, libc)
		if err != nil {
			t.Fatalf("runtimeIdentityHash(%q): %v", name, err)
		}
		if want := env.GetRuntimeDataPath(name, identity); root != want {
			t.Errorf("RuntimeOwnedEnvironment(%q) root = %q, want %q", name, root, want)
		}
		for key, value := range vars {
			switch key {
			case "GOBIN":
				if value != filepath.Join(appEnvPath, "bin") {
					t.Errorf("GOBIN = %q, want app-local path", value)
				}
			case "GOPATH", "GOMODCACHE", "GOCACHE", "npm_config_store_dir", "npm_config_virtual_store_dir", "npm_config_global_dir", "npm_config_cache", "NODE_COMPILE_CACHE", "BUN_RUNTIME_TRANSPILER_CACHE_PATH", "UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR":
				if !filepath.IsAbs(value) {
					t.Errorf("%s = %q, want an absolute runtime-owned path", key, value)
				}
			}
		}
	}
	_, runtimeOnlyGoEnv, err := rm.RuntimeOwnedEnvironment("go", runtimes["go"], "")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := runtimeOnlyGoEnv["GOBIN"]; exists {
		t.Fatalf("runtime-only Go environment unexpectedly sets GOBIN: %q", runtimeOnlyGoEnv["GOBIN"])
	}

	if root, vars, err := rm.RuntimeOwnedEnvironment("jvm", config.RuntimeConfig{Kind: config.RuntimeKindJVM}, appEnvPath); err != nil || root != "" || vars != nil {
		t.Fatalf("JVM runtime-owned env = (%q, %v, %v), want no env or data path", root, vars, err)
	}
}

func TestRuntimeDefinitionsReturnsTypedConfigSnapshot(t *testing.T) {
	want := bunSystemRuntime("bun")
	rm := New(want)
	data := rm.RuntimeDefinitions()
	if len(data) == 0 {
		t.Fatal("RuntimeDefinitions returned empty JSON")
	}
	var got config.MapOfRuntimes
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode RuntimeDefinitions: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RuntimeDefinitions = %#v, want %#v", got, want)
	}
}

func TestSystemRuntimeDataIdentityIncludesTargetPlatform(t *testing.T) {
	rc := config.RuntimeConfig{Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "uv", SystemVersion: "0.13.6"}, UV: &config.RuntimeConfigUV{PythonVersion: "3.14.6"}}
	rm := New(config.MapOfRuntimes{"uv": rc})
	linux, err := rm.runtimeIdentityHash(rc, "linux", "amd64", "glibc")
	if err != nil {
		t.Fatal(err)
	}
	darwin, err := rm.runtimeIdentityHash(rc, "darwin", "arm64", "none")
	if err != nil {
		t.Fatal(err)
	}
	if linux == darwin {
		t.Fatalf("system runtime identity ignored target platform: %q", linux)
	}
}

func TestRuntimeInstallResultJSONRoundTrip(t *testing.T) {
	for _, result := range []RuntimeInstallResult{
		{Name: "node"},
		{Name: "uv", Error: errors.New("python pin missing")},
	} {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		var decoded RuntimeInstallResult
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if decoded.Name != result.Name || (decoded.Error == nil) != (result.Error == nil) || (result.Error != nil && decoded.Error.Error() != result.Error.Error()) {
			t.Fatalf("round-trip = %#v, want %#v", decoded, result)
		}
	}
	var result RuntimeInstallResult
	if err := json.Unmarshal([]byte("{"), &result); err == nil {
		t.Fatal("malformed runtime result JSON was accepted")
	}
}

func TestComputeRuntimeDataPathRejectsUnknownRuntime(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(config.MapOfRuntimes{})
	if _, err := rm.ComputeRuntimeDataPath("missing"); err == nil {
		t.Fatal("ComputeRuntimeDataPath accepted an unknown runtime")
	}
	if _, _, err := rm.RuntimeExecutionEnvironment("pnpm", testPNPMRuntime()); err == nil {
		t.Fatal("RuntimeExecutionEnvironment accepted a package-manager-only runtime")
	}
}

func TestRuntimeOwnedEnvironmentRejectsUnownedRuntimeKinds(t *testing.T) {
	rm := New(nil)
	for _, kind := range []config.RuntimeKind{config.RuntimeKindPNPM, config.RuntimeKind("unknown")} {
		if _, _, err := rm.RuntimeOwnedEnvironment("custom", config.RuntimeConfig{Kind: kind}, ""); err == nil {
			t.Errorf("RuntimeOwnedEnvironment accepted kind %q", kind)
		}
		if _, _, err := rm.RuntimeExecutionEnvironment("custom", config.RuntimeConfig{Kind: kind}); err == nil {
			t.Errorf("RuntimeExecutionEnvironment accepted kind %q", kind)
		}
	}
}
