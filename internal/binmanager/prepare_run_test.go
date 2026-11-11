package binmanager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/target"
)

// stepAppBinary builds a small Go program serving as the app's binary: run
// with the argument PREPARE it writes a file into ${APP_PREPARED}; any other
// invocation exits 0. Go programs run on every platform, so the test needs
// no POSIX fixtures.
func stepAppBinary(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "stepmain.go")
	source := `package main

import ("os"; "path/filepath")

func main() {
	if len(os.Args) > 1 && os.Args[1] == "PREPARE" {
		dir := os.Getenv("STEP_OUT")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "done"), []byte("yes"), 0o644)
	}
}
`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "stepapp.bin")
	if out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build step binary: %v: %s", err, out)
	}
	return bin
}

// stepAppRegistry returns a registry whose "stepper" app is a locally built
// binary declaring a prepare step that writes into its prepared state, with
// runtimeEnv exposing the result.

// seedStepBinary writes the built binary at its store path so install paths
// never touch the unreachable fixture URL.
func seedStepBinary(t *testing.T, bm *BinManager, bin string) {
	t.Helper()
	binPath, err := bm.getBinaryPath("stepper")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func stepAppRegistry(t *testing.T) (MapOfApps, string) {
	t.Helper()
	bin := stepAppBinary(t, t.TempDir())
	no := false
	apps := MapOfApps{
		"stepper": {
			Binary: &AppConfigBinary{Binaries: MapOfBinaries{
				"linux": {"amd64": {"glibc": {URL: "https://example.invalid/stepper", Hash: "sha256:" + strings.Repeat("0", 64), ContentType: BinContentTypeBinary}}},
			}},
			Prepare: &AppPrepare{
				Args:    []string{"PREPARE"},
				Env:     map[string]string{"STEP_OUT": "${APP_PREPARED}"},
				Network: &no,
			},
			RuntimeEnv: map[string]string{"STEPPER_STATE": "${APP_PREPARED}"},
		},
	}
	return apps, bin
}

func stepTestManager(t *testing.T, apps MapOfApps) *BinManager {
	t.Helper()
	resolver := target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc})
	bm := NewWithResolver(apps, nil, nil, resolver)
	return bm
}

func TestGetCommandInfoPreparesOnce(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps, built := stepAppRegistry(t)
	bm := stepTestManager(t, apps)
	seedStepBinary(t, bm, built)

	info, err := bm.GetCommandInfo(t.Context(), "stepper")
	if err != nil {
		t.Fatalf("GetCommandInfo: %v", err)
	}

	// The marker and the state exist.
	ready, err := bm.PreparedStateReady("stepper")
	if err != nil || !ready {
		t.Fatalf("PreparedStateReady = %v, %v; want true", ready, err)
	}
	// runtimeEnv's ${APP_PREPARED} resolved to the placed state.
	state := info.Env["STEPPER_STATE"]
	if !strings.Contains(state, filepath.Join(".prepared", "stepper")) {
		t.Fatalf("STEPPER_STATE = %q, want the placed .prepared path", state)
	}
	if _, err := os.Stat(filepath.Join(state, "done")); err != nil {
		t.Fatalf("prepared output missing: %v", err)
	}

	// A second resolution runs no step again: same key, marker present.
	if err := bm.EnsurePrepared(t.Context(), "stepper"); err != nil {
		t.Fatalf("second EnsurePrepared: %v", err)
	}
}

func TestInstallOnlySkipsPrepare(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps, built := stepAppRegistry(t)
	bm := stepTestManager(t, apps)
	seedStepBinary(t, bm, built)

	if _, err := bm.GetCommandInfoInstallOnly(t.Context(), "stepper"); err != nil {
		t.Fatalf("GetCommandInfoInstallOnly: %v", err)
	}
	ready, err := bm.PreparedStateReady("stepper")
	if err != nil {
		t.Fatalf("PreparedStateReady: %v", err)
	}
	if ready {
		t.Fatal("install-only path ran the prepare step")
	}
}

func TestResolveCommandInfoGatesOnMarker(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps, built := stepAppRegistry(t)
	bm := stepTestManager(t, apps)

	// Install the binary (writing it directly avoids the unreachable URL).
	seedStepBinary(t, bm, built)

	// Unprepared: the marker is a health path, so installed is false.
	_, installed, err := bm.ResolveCommandInfo("stepper")
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("unprepared app reported installed")
	}

	// After preparation the same resolution reports installed.
	if err := bm.EnsurePrepared(t.Context(), "stepper"); err != nil {
		t.Fatalf("EnsurePrepared: %v", err)
	}
	_, installed, err = bm.ResolveCommandInfo("stepper")
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("prepared app not reported installed")
	}
}

func TestEnsureToolsPreparesAfterBarrier(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps, built := stepAppRegistry(t)
	bm := stepTestManager(t, apps)

	seedStepBinary(t, bm, built)

	if err := bm.EnsureTools(t.Context(), []string{"stepper"}); err != nil {
		t.Fatalf("EnsureTools: %v", err)
	}
	ready, err := bm.PreparedStateReady("stepper")
	if err != nil || !ready {
		t.Fatalf("EnsureTools left the app unprepared: ready=%v err=%v", ready, err)
	}
}

func TestResolveBindingPathByAlias(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	pin := "sha256:" + strings.Repeat("0", 64)
	apps := MapOfApps{
		"root": {
			Shell:     &AppConfigShell{Name: "echo"},
			DependsOn: []AppDependency{{App: "dep", Alias: "validator"}},
		},
		"dep": {Binary: testBinaryApp("https://example.invalid/dep", pin)},
	}
	bm := stepTestManager(t, apps)

	path, err := bm.ResolveBindingPath(t.Context(), "root", "validator")
	if err != nil {
		t.Fatalf("ResolveBindingPath: %v", err)
	}
	want, err := bm.getBinaryPath("dep")
	if err != nil {
		t.Fatal(err)
	}
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	// An alias that no edge carries is an error.
	if _, err := bm.ResolveBindingPath(t.Context(), "root", "dep"); err == nil {
		t.Fatal("app-name lookup accepted where only the alias is bound")
	}
	_ = env.GetStorePath // keep the import when fixtures change
}

func TestStepCommandExpandsArgsAndKeepsPrefix(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps, _ := stepAppRegistry(t)
	// Give the step an argument with bindings and ${APP_DIR}.
	stepper := apps["stepper"]
	stepper.Prepare.Args = []string{"--dir", "${APP_DIR}/x", "--cache", "${APP_PREPARED}/sub"}
	apps["stepper"] = stepper
	bm := stepTestManager(t, apps)

	argv, err := bm.stepCommand(t.Context(), "stepper", apps["stepper"])
	if err != nil {
		t.Fatalf("stepCommand: %v", err)
	}
	if len(argv) < 4 {
		t.Fatalf("argv = %v", argv)
	}
	if argv[2] == "${APP_DIR}/x" {
		t.Fatal("${APP_DIR} not expanded in args")
	}
	if argv[4] != "${APP_PREPARED}/sub" {
		t.Fatalf("arg = %q, want symbolic ${APP_PREPARED}", argv[4])
	}
}

func TestStepEnvironmentRuntimeKeysWin(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	apps, _ := stepAppRegistry(t)
	stepper := apps["stepper"]
	stepper.Env = map[string]string{"CUSTOM": "app", "SHARED": "from-app"}
	stepper.Prepare.Env = map[string]string{"PREP_ONLY": "yes", "SHARED": "from-prepare"}
	apps["stepper"] = stepper
	bm := stepTestManager(t, apps)

	envList, err := bm.stepEnvironment(t.Context(), "stepper", apps["stepper"])
	if err != nil {
		t.Fatalf("stepEnvironment: %v", err)
	}
	got := map[string]string{}
	for _, kv := range envList {
		if k, v, ok := strings.Cut(kv, "="); ok {
			got[k] = v
		}
	}
	if got["CUSTOM"] != "app" {
		t.Fatalf("app.env lost: %v", got)
	}
	if got["PREP_ONLY"] != "yes" {
		t.Fatalf("prepare.env lost: %v", got)
	}
	if got["SHARED"] != "from-prepare" {
		t.Fatalf("prepare.env must beat app.env: %v", got["SHARED"])
	}
	if got["DATAMITSU_CACHE_DIR"] == "" || filepath.Base(got["DATAMITSU_CACHE_DIR"]) == "cache" {
		t.Fatalf("DATAMITSU_CACHE_DIR = %q, want the base dir (not the cache subdir)", got["DATAMITSU_CACHE_DIR"])
	}
	// A dependency-less binary app declares nothing: the strict PATH is
	// empty and the host tail must NOT leak in (declared-only contract).
	if got["PATH"] != "" {
		t.Fatalf("PATH = %q, want empty in declared mode for a dependency-less binary app", got["PATH"])
	}
	if got["TMPDIR"] != "${STEP_TMPDIR}" {
		t.Fatalf("TMPDIR = %q, want symbolic", got["TMPDIR"])
	}
}

func TestDependencyLauncherEnvChangeRebuildsPreparedOutput(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	stepDir := t.TempDir()
	source := `package main

import ("fmt"; "os"; "os/exec"; "path/filepath")

func main() {
	if len(os.Args) < 2 { return }
	switch os.Args[1] {
	case "FRONTEND":
		fmt.Print(os.Getenv("REVIEW_MODE"))
	case "PREPARE":
		out, err := exec.Command(os.Getenv("FRONTEND_BIN")).CombinedOutput()
		if err != nil { fmt.Fprintf(os.Stderr, "frontend: %v: %s", err, out); os.Exit(1) }
		if err := os.MkdirAll(os.Getenv("STEP_OUT"), 0o755); err != nil { panic(err) }
		if err := os.WriteFile(filepath.Join(os.Getenv("STEP_OUT"), "mode"), out, 0o644); err != nil { panic(err) }
	}
}
`
	src := filepath.Join(stepDir, "stepmain.go")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	stepBinary := filepath.Join(stepDir, "stepper.bin")
	if out, err := exec.Command("go", "build", "-o", stepBinary, src).CombinedOutput(); err != nil {
		t.Fatalf("build step binary: %v: %s", err, out)
	}
	apps := MapOfApps{
		"stepper": {
			Binary: &AppConfigBinary{Binaries: MapOfBinaries{
				"linux": {"amd64": {"glibc": {URL: "https://example.invalid/stepper", Hash: "sha256:" + strings.Repeat("0", 64), ContentType: BinContentTypeBinary}}},
			}},
			DependsOn: edges("frontend"),
			Prepare: &AppPrepare{
				Args: []string{"PREPARE"},
				Env:  map[string]string{"STEP_OUT": "${APP_PREPARED}", "FRONTEND_BIN": "${APP_BIN:frontend}"},
			},
		},
		"frontend": {
			Node:       &AppConfigNode{PackageName: "frontend", Version: "1", BinPath: "entrypoint.mjs"},
			RuntimeEnv: map[string]string{"REVIEW_MODE": "strict"},
		},
	}
	bm := stepTestManager(t, apps)
	bm.runtimeManager = &mockRuntimeAppManager{
		getCommandInfoFunc: func(context.Context, string, App) (*CommandInfo, error) {
			return &CommandInfo{Command: stepBinary, Args: []string{"FRONTEND"}}, nil
		},
		resolveCommandInfoFunc: func(string, App) (*CommandInfo, error) {
			return &CommandInfo{Command: stepBinary, Args: []string{"FRONTEND"}}, nil
		},
		computeAppPathFunc: func(string, App) (string, error) {
			return filepath.Join(env.GetStorePath(), ".apps", "node", "frontend"), nil
		},
	}
	seedStepBinary(t, bm, stepBinary)

	readPreparedMode := func() string {
		t.Helper()
		key, err := bm.ComputePreparedKey("stepper")
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(PreparePath(), "stepper", key, "out", "mode"))
		if err != nil {
			t.Fatalf("read prepared mode: %v", err)
		}
		return string(data)
	}
	if _, err := bm.GetCommandInfo(t.Context(), "stepper"); err != nil {
		t.Fatalf("initial prepare: %v", err)
	}
	if got := readPreparedMode(); got != "strict" {
		t.Fatalf("initial prepared mode = %q, want strict", got)
	}

	frontend := bm.mapOfApps["frontend"]
	frontend.RuntimeEnv["REVIEW_MODE"] = "permissive"
	bm.mapOfApps["frontend"] = frontend
	if _, err := bm.GetCommandInfo(t.Context(), "stepper"); err != nil {
		t.Fatalf("prepare after dependency launcher change: %v", err)
	}
	if got := readPreparedMode(); got != "permissive" {
		t.Fatalf("prepared mode after launcher change = %q, want permissive", got)
	}
}
