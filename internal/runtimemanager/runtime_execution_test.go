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

func TestEnsureExecutionRejectsPackageManagerAndMissingSystemRuntime(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(config.MapOfRuntimes{
		"pnpm":         testPNPMRuntime(),
		"missing-node": {Kind: config.RuntimeKindNode, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "absent-node-command"}, Node: &config.RuntimeConfigNode{NodeVersion: "22", PNPMRuntime: testPNPMRuntimeName}},
	})
	if _, err := rm.EnsureExecution(context.Background(), "pnpm"); err == nil || !strings.Contains(err.Error(), "package manager, not a directly runnable interpreter") {
		t.Fatalf("EnsureExecution(pnpm) = %v, want package-manager explanation", err)
	}
	if _, err := rm.EnsureExecution(context.Background(), "missing-node"); err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("EnsureExecution(missing system runtime) = %v, want missing executable diagnostic", err)
	}
}

func TestResolveExecutionRejectsUnknownAndMalformedRuntimeKind(t *testing.T) {
	rm := New(config.MapOfRuntimes{"broken": {Kind: config.RuntimeKind("unknown")}})
	if _, err := rm.ResolveExecution("absent"); !errors.Is(err, ErrUnknownRuntime) {
		t.Fatalf("unknown runtime error = %v", err)
	}
	if _, err := rm.ResolveExecution("broken"); err == nil || !strings.Contains(err.Error(), "runtime kind \"unknown\" is not directly runnable") {
		t.Fatalf("unsupported kind error = %v", err)
	}
}

func TestRuntimeInventorySeparatesAvailabilityAndRunnableReasons(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"bad/name":    {Kind: config.RuntimeKindNode, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "node"}},
		"no-platform": {Kind: config.RuntimeKindGo, Mode: config.RuntimeModeManaged, Managed: &config.RuntimeConfigManaged{}},
		"jvm-system":  {Kind: config.RuntimeKindJVM, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "java", SystemVersion: "21"}, JVM: &config.RuntimeConfigJVM{JavaVersion: "21"}},
		"bun":         {Kind: config.RuntimeKindBun, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "bun"}, Bun: &config.RuntimeConfigBun{BunVersion: "1"}},
		"pnpm":        testPNPMRuntime(),
	}
	rm := New(runtimes)
	rm.lookPathFunc = func(command string) (string, error) {
		if command == "java" || command == "bun" {
			return "/runtime/" + command, nil
		}
		return "", errors.New("not found")
	}
	entries, err := rm.RuntimeInventory()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]RuntimeInventoryEntry, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	if got := byName["bad/name"]; got.Status != RuntimeStatusUnavailable || got.Reason == "" || got.NotRunnableReason != "" {
		t.Errorf("unsafe name status = %#v", got)
	}
	if got := byName["no-platform"]; got.Status != RuntimeStatusUnavailable || got.Reason == "" || got.NotRunnableReason != "" {
		t.Errorf("unavailable managed target status = %#v", got)
	}
	if got := byName["jvm-system"]; got.Status != RuntimeStatusInstalled || got.Version != "21" || got.Executable != "/runtime/java" {
		t.Errorf("system JVM inventory = %#v", got)
	}
	if got := byName["bun"]; got.Status != RuntimeStatusInstalled || !got.Runnable || got.Version != "1" || got.Reason != "" || got.NotRunnableReason != "" {
		t.Errorf("installed system Bun inventory = %#v", got)
	}
	if got := byName["pnpm"]; got.Runnable || got.NotRunnableReason == "" || got.Status != RuntimeStatusMissing || got.Reason == "" {
		t.Errorf("pnpm availability and runnable metadata = %#v", got)
	}
}

func TestRuntimeConfiguredVersionUsesSystemOverrideThenKindVersion(t *testing.T) {
	tests := []struct {
		name string
		rc   config.RuntimeConfig
		want string
	}{
		{name: "node", rc: config.RuntimeConfig{Kind: config.RuntimeKindNode, Node: &config.RuntimeConfigNode{NodeVersion: "22"}}, want: "22"},
		{name: "bun", rc: config.RuntimeConfig{Kind: config.RuntimeKindBun, Bun: &config.RuntimeConfigBun{BunVersion: "1.2"}}, want: "1.2"},
		{name: "uv", rc: config.RuntimeConfig{Kind: config.RuntimeKindUV, UV: &config.RuntimeConfigUV{PythonVersion: "3.14.6"}}, want: "3.14.6"},
		{name: "go", rc: config.RuntimeConfig{Kind: config.RuntimeKindGo, Go: &config.RuntimeConfigGo{GoVersion: "1.25"}}, want: "1.25"},
		{name: "jvm", rc: config.RuntimeConfig{Kind: config.RuntimeKindJVM, JVM: &config.RuntimeConfigJVM{JavaVersion: "25"}}, want: "25"},
		{name: "pnpm", rc: config.RuntimeConfig{Kind: config.RuntimeKindPNPM, PNPM: &config.RuntimeConfigPNPM{PNPMVersion: "12"}}, want: "12"},
		{name: "system override", rc: config.RuntimeConfig{Kind: config.RuntimeKindNode, System: &config.RuntimeConfigSystem{SystemVersion: "system-current"}, Node: &config.RuntimeConfigNode{NodeVersion: "configured"}}, want: "system-current"},
		{name: "missing version", rc: config.RuntimeConfig{Kind: config.RuntimeKind("unknown")}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeConfiguredVersion(tt.rc); got != tt.want {
				t.Fatalf("runtimeConfiguredVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
