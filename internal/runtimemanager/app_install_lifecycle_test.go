package runtimemanager

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

const appInstallHelperSource = `package main
import (
 "encoding/json"
 "os"
 "path/filepath"
 "runtime"
 "strings"
)
func main() {
 cwd, _ := os.Getwd()
 kind := os.Getenv("DATAMITSU_HELPER_KIND")
 pkg := os.Getenv("DATAMITSU_HELPER_PACKAGE")
 mode := os.Getenv("DATAMITSU_APP_INSTALL_MODE")
 env := map[string]string{}
 for _, key := range []string{"UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR", "UV_PYTHON_DOWNLOADS", "npm_config_cache", "npm_config_store_dir", "npm_config_global_dir", "NODE_COMPILE_CACHE", "BUN_RUNTIME_TRANSPILER_CACHE_PATH"} { env[key] = os.Getenv(key) }
 state := map[string]any{"kind": kind, "package": pkg, "mode": mode, "args": os.Args[1:], "env": env}
 if data, err := json.Marshal(state); err == nil { _ = os.WriteFile(os.Getenv("DATAMITSU_HELPER_RECORD"), data, 0600) }
 if mode == "fail" { os.Exit(19) }
 if mode == "lock" {
  filename, content := "pnpm-lock.yaml", "lockfileVersion: '9.0'\n"
  if kind == "uv" { filename, content = "uv.lock", "version = 1\n" }
  _ = os.WriteFile(filepath.Join(cwd, filename), []byte(content+"# workspace="+cwd+"\n"), 0600)
 }
 if kind == "uv" {
  venvDir := "bin"
  pythonName := "python"
  executableName := pkg
  if runtime.GOOS == "windows" { venvDir = "Scripts"; pythonName = "python.exe"; executableName += ".exe" }
  venvBin := filepath.Join(cwd, ".venv", venvDir)
  _ = os.MkdirAll(venvBin, 0755)
  python, err := os.ReadFile(os.Getenv("UV_HELPER_PYTHON"))
  if err == nil { _ = os.WriteFile(filepath.Join(venvBin, pythonName), python, 0755) }
  _ = os.WriteFile(filepath.Join(venvBin, executableName), []byte("wrapper"), 0755)
  os.Stdout.Write([]byte("{\"summary\":{\"changes\":[{\"action\":\"installed\"}]}}"))
  return
 }
 _ = os.MkdirAll(filepath.Join(cwd, "node_modules", pkg), 0755)
 _ = os.MkdirAll(filepath.Join(cwd, "bin"), 0755)
 _ = os.WriteFile(filepath.Join(cwd, "node_modules", pkg, "package.json"), []byte("{\"name\":\"probe-pkg\"}"), 0644)
 _ = os.WriteFile(filepath.Join(cwd, "bin", "probe"), []byte("probe"), 0755)
 os.Stderr.Write([]byte("{\"name\":\"pnpm:progress\",\"level\":\"debug\"}\n"))
 if strings.Contains(mode, "fail") { os.Exit(19) }
}
`

func compileAppInstallHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(appInstallHelperSource), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "installer-helper")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile installer helper: %v\n%s", err, out)
	}
	return binary
}

func copyInstallerHelper(t *testing.T, binary, destination string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, 0o755); err != nil {
		t.Fatal(err)
	}
	return destination
}

func TestInstallUVAppRealLifecycle(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	helper := compileAppInstallHelper(t)
	uvPath := copyInstallerHelper(t, helper, filepath.Join(base, "uv-bin", "uv"+executableSuffix()))
	rc := testUVRuntimeConfig(t, uvPath, "3.14.6")
	rm := New(config.MapOfRuntimes{"uv": rc})
	pythonPath := seedTestPythonReady(t, rm, "uv", "", "")
	record := filepath.Join(base, "uv-record.json")
	app := &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.37.0", Runtime: "uv", RequiresPython: ">=3.12"}
	appPath, err := rm.GetAppPath("yamllint", config.RuntimeKindUV, uvVersionForHash(app.Version, app.RequiresPython), nil, "", nil, nil, "uv")
	if err != nil {
		t.Fatal(err)
	}
	customEnv := map[string]string{"UV_CACHE_DIR": "wrong", "UV_PYTHON_DOWNLOADS": "yes"}
	t.Setenv("DATAMITSU_HELPER_RECORD", record)
	t.Setenv("DATAMITSU_HELPER_KIND", "uv")
	t.Setenv("DATAMITSU_HELPER_PACKAGE", app.PackageName)
	t.Setenv("UV_HELPER_PYTHON", pythonPath)
	t.Setenv("DATAMITSU_APP_INSTALL_MODE", "success")
	if err := rm.InstallUVApp(context.Background(), "yamllint", app, customEnv, nil, nil); err != nil {
		t.Fatalf("InstallUVApp: %v", err)
	}
	if !appReady(appPath) || !uvVenvHealthy(appPath, getUVBinaryPath(appPath, "yamllint")) {
		t.Fatal("successful uv sync did not publish a healthy ready app")
	}
	gotPython, err := os.ReadFile(getUVInterpreterPath(appPath))
	if err != nil {
		t.Fatal(err)
	}
	wantPython, err := os.ReadFile(pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotPython) != string(wantPython) {
		t.Fatal("uv installer did not bind the venv interpreter to the pinned Python payload")
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	runtimeData, expectedEnv, err := rm.RuntimeOwnedEnvironment("uv", rc, appPath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Env["UV_PYTHON_DOWNLOADS"] != "never" || state.Env["UV_CACHE_DIR"] != expectedEnv["UV_CACHE_DIR"] || state.Env["UV_CACHE_DIR"] == "wrong" || state.Env["UV_PYTHON_INSTALL_DIR"] != expectedEnv["UV_PYTHON_INSTALL_DIR"] || state.Env["UV_CACHE_DIR"] != filepath.Join(runtimeData, "uv-cache") {
		t.Fatalf("uv installer did not receive owned Python/cache settings: got %#v want %#v", state.Env, expectedEnv)
	}
	foundPythonArg := false
	for i := range state.Args {
		if state.Args[i] == "--python" && i+1 < len(state.Args) && state.Args[i+1] == pythonPath {
			foundPythonArg = true
		}
	}
	if !foundPythonArg {
		t.Fatalf("uv sync args did not pin Python %q: %v", pythonPath, state.Args)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if err := rm.InstallUVApp(context.Background(), "yamllint", app, customEnv, nil, nil); err != nil {
		t.Fatalf("repeat InstallUVApp: %v", err)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("healthy repeat invoked uv installer: %v", err)
	}
}

func TestInstallNodeAppRealPNPMLifecycle(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	helper := compileAppInstallHelper(t)
	pnpm := copyInstallerHelper(t, helper, filepath.Join(base, "pnpm-bin", "pnpm"+executableSuffix()))
	record := filepath.Join(base, "pnpm-record.json")
	runtimes := config.MapOfRuntimes{
		"node": {Kind: config.RuntimeKindNode, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: pnpm, SystemVersion: "22"}, Node: &config.RuntimeConfigNode{NodeVersion: "22", PNPMRuntime: "pnpm"}},
		"pnpm": systemPNPMRuntime(pnpm),
	}
	rm := New(runtimes)
	app := &binmanager.AppConfigNode{PackageName: "probe-pkg", Version: "1.2.3", BinPath: "bin/probe", Runtime: "node"}
	t.Setenv("DATAMITSU_HELPER_RECORD", record)
	t.Setenv("DATAMITSU_HELPER_KIND", "pnpm")
	t.Setenv("DATAMITSU_HELPER_PACKAGE", app.PackageName)
	t.Setenv("DATAMITSU_APP_INSTALL_MODE", "success")
	t.Setenv("npm_config_cache", "inherited-wrong")
	if err := rm.InstallNodeApp(context.Background(), "probe", app, nil, nil, nil); err != nil {
		t.Fatalf("InstallNodeApp: %v", err)
	}
	appPath, err := rm.resolveNodeAppEnvPath("probe", app, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !appReady(appPath) {
		t.Fatal("successful pnpm install did not publish app-ready marker")
	}
	for _, p := range []string{filepath.Join(appPath, "bin", "probe"), filepath.Join(appPath, "node_modules", "probe-pkg", "package.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("installer output missing %s: %v", p, err)
		}
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	runtimeData, expectedEnv, err := rm.RuntimeOwnedEnvironment("node", runtimes["node"], appPath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Env["npm_config_cache"] != expectedEnv["npm_config_cache"] || state.Env["npm_config_cache"] == "inherited-wrong" || state.Env["npm_config_store_dir"] != expectedEnv["npm_config_store_dir"] || state.Env["npm_config_global_dir"] == "" || state.Env["npm_config_cache"] != filepath.Join(runtimeData, "npm-cache") {
		t.Fatalf("pnpm install did not receive named runtime-owned caches: got %#v want %#v", state.Env, expectedEnv)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if err := rm.InstallNodeApp(context.Background(), "probe", app, nil, nil, nil); err != nil {
		t.Fatalf("repeat InstallNodeApp: %v", err)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("healthy repeat invoked pnpm installer: %v", err)
	}
	t.Setenv("DATAMITSU_APP_INSTALL_MODE", "fail")
	if err := rm.InstallNodeApp(context.Background(), "probe-failure", app, nil, nil, nil); err == nil {
		t.Fatal("expected helper installation failure")
	}
	failedPath, err := rm.resolveNodeAppEnvPath("probe-failure", app, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(failedPath); !os.IsNotExist(err) {
		t.Fatalf("failed installation left partial app tree: %v", err)
	}
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
