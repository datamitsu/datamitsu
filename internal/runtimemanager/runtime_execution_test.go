package runtimemanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
)

func TestResolveExecutionIsReadOnlyAndReportsMissingManagedRuntime(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	runtimes := nodeRuntimeWith(t, "https://example.invalid/node.tar.xz", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", testLibc)
	runtimes["../../escape"] = runtimes["node"]
	rm := New(runtimes)

	got, err := rm.ResolveExecution("node")
	if err != nil {
		t.Fatalf("ResolveExecution() error = %v", err)
	}
	if got.Kind != config.RuntimeKindNode || got.Status != RuntimeStatusMissing || got.Executable == "" || got.Identity == "" || got.DataRoot == "" {
		t.Fatalf("ResolveExecution() = %#v, want a missing Node runtime with resolved paths", got)
	}
	if _, err := os.Stat(got.DataRoot); !os.IsNotExist(err) {
		t.Fatalf("resolution created runtime data root %q: %v", got.DataRoot, err)
	}
	if _, err := os.Stat(got.Executable); !os.IsNotExist(err) {
		t.Fatalf("resolution unexpectedly materialized runtime executable %q: %v", got.Executable, err)
	}

	if _, err := rm.ResolveExecution("../../escape"); err == nil || !strings.Contains(err.Error(), "safe store path component") {
		t.Fatalf("unsafe runtime name error = %v", err)
	}
	if _, err := rm.ResolveExecution("absent"); !errors.Is(err, ErrUnknownRuntime) {
		t.Fatalf("unknown runtime error = %v, want ErrUnknownRuntime", err)
	}
}

func TestRuntimeInventoryIsSortedAndDoesNotMaterialize(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	rm := New(config.MapOfRuntimes{
		"z-go":   {Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "definitely-not-a-real-go-command"}, Go: &config.RuntimeConfigGo{GoVersion: "1.23.0"}},
		"a-uv":   {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "definitely-not-a-real-uv-command"}, UV: &config.RuntimeConfigUV{PythonVersion: "3.14.6"}},
		"b-pnpm": testPNPMRuntime(),
	})
	got, err := rm.RuntimeInventory()
	if err != nil {
		t.Fatalf("RuntimeInventory() error = %v", err)
	}
	wantNames := []string{"a-uv", "b-pnpm", "z-go"}
	names := make([]string, 0, len(got))
	for _, entry := range got {
		names = append(names, entry.Name)
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("inventory names = %v, want %v", names, wantNames)
	}
	if got[0].Version != "3.14.6" || got[0].Status != RuntimeStatusMissing || got[0].Executable != "" {
		t.Errorf("uv inventory = %#v, want pinned Python version and missing status", got[0])
	}
	if got[1].Runnable || got[1].Status != RuntimeStatusMissing {
		t.Errorf("pnpm inventory = %#v, want non-runnable missing binary", got[1])
	}
	if got[2].Status != RuntimeStatusMissing {
		t.Errorf("missing system Go inventory = %#v, want missing", got[2])
	}
	if _, err := os.Stat(filepath.Join(env.GetStorePath(), ".runtime-data")); !os.IsNotExist(err) {
		t.Fatalf("inventory created runtime data: %v", err)
	}
	if got, err := New(nil).RuntimeInventory(); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty inventory = %#v, %v; want non-nil empty slice", got, err)
	}
}

func TestEnsureExecutionUsesParentCancellation(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")
	runtimes := nodeRuntimeWith(t, "https://example.invalid/node.tar.xz", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", testLibc)
	rm := New(runtimes)
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := rm.EnsureExecution(parent, "node")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureExecution() error = %v, want parent context cancellation", err)
	}
	if _, err := os.Stat(filepath.Join(env.GetStorePath(), ".runtimes", "node")); !os.IsNotExist(err) {
		t.Fatalf("canceled acquisition mutated runtime store: %v", err)
	}
}

func TestResolveExecutionRuntimeEnvironmentReachesInterpreterProcess(t *testing.T) {
	if os.Getenv("DATAMITSU_RUNTIME_EXECUTION_PROBE") == "1" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"gocache":    os.Getenv("GOCACHE"),
			"gomodcache": os.Getenv("GOMODCACHE"),
			"gopath":     os.Getenv("GOPATH"),
		})
		os.Exit(0)
	}
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)

	t.Setenv("DATAMITSU_RUNTIME_EXECUTION_PROBE", "")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rm := New(config.MapOfRuntimes{
		"go-host": {Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: binary, SystemVersion: "go-test"}, Go: &config.RuntimeConfigGo{GoVersion: "go-test"}},
	})
	execution, err := rm.ResolveExecution("go-host")
	if err != nil {
		t.Fatalf("ResolveExecution() error = %v", err)
	}
	if execution.Status != RuntimeStatusInstalled || execution.Executable != binary {
		t.Fatalf("execution = %#v, want installed test interpreter", execution)
	}
	cmd := exec.Command(execution.Executable, "-test.run=^TestResolveExecutionRuntimeEnvironmentReachesInterpreterProcess$")
	cmd.Env = append(os.Environ(), "DATAMITSU_RUNTIME_EXECUTION_PROBE=1")
	for key, value := range execution.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("execute resolved interpreter probe: %v", err)
	}
	var observed map[string]string
	if err := json.Unmarshal(output, &observed); err != nil {
		t.Fatalf("decode probe output %q: %v", output, err)
	}
	for key, suffix := range map[string]string{"gocache": "go-build", "gomodcache": "go-modules", "gopath": "go-path"} {
		want := filepath.Join(execution.DataRoot, suffix)
		if observed[key] != want {
			t.Errorf("child %s = %q, want %q", key, observed[key], want)
		}
	}
	if _, err := os.Stat(execution.DataRoot); !os.IsNotExist(err) {
		t.Fatalf("resolution created runtime data root: %v", err)
	}
}

func TestEnsureExecutionUsesReadyPythonOfflineWithoutUV(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")
	rm := New(config.MapOfRuntimes{"python": testUVRuntimeConfig(t, "uv-not-installed", "3.14.6")})
	python := seedTestPythonReady(t, rm, "python", "", "")
	execution, err := rm.EnsureExecution(context.Background(), "python")
	if err != nil {
		t.Fatalf("EnsureExecution() with ready Python offline: %v", err)
	}
	if execution.Status != RuntimeStatusInstalled || execution.Executable != python || execution.Kind != config.RuntimeKindUV {
		t.Fatalf("execution = %#v, want ready pinned Python %q", execution, python)
	}
}
