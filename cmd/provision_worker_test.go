package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "--internal-provision-worker" {
		if err := runProvisionWorker(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func TestHandleProvisionOperationRejectsMalformedRequestsBeforeMutation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	store, cache := env.GetStorePath(), env.GetCachePath()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		operation string
		payload   json.RawMessage
		contains  string
	}{
		{"malformed JSON", "install-runtimes", json.RawMessage(`{"storeRoot":`), "decode install-runtimes"},
		{"missing store", "install-runtimes", workerPayload(t, runtimemanager.InstallRuntimesRequest{CacheRoot: cache, Runtimes: config.MapOfRuntimes{}, Names: []string{}}), "worker store root is required"},
		{"missing runtime definition", "ensure-execution", workerPayload(t, runtimemanager.EnsureExecutionRequest{StoreRoot: store, CacheRoot: cache, Runtimes: config.MapOfRuntimes{}, Name: "missing"}), "unknown runtime"},
		{"malformed runtime definitions", "install-apps", workerPayload(t, map[string]any{"storeRoot": store, "cacheRoot": cache, "action": "install", "runtimeDefinitions": "{broken"}), "decode runtime definitions"},
		{"malformed runtime request", "install-runtimes", workerPayload(t, map[string]any{"storeRoot": store, "cacheRoot": cache, "runtimes": "not-a-runtime-map"}), "decode install-runtimes request"},
		{"malformed lock request", "generate-lock", workerPayload(t, map[string]any{"storeRoot": store, "cacheRoot": cache, "app": "not-an-app"}), "decode generate-lock request"},
		{"malformed prepare request", "prepare-apps", workerPayload(t, map[string]any{"storeRoot": store, "cacheRoot": cache, "apps": "not-an-app-map"}), "decode prepare-apps request"},
		{"malformed parser request", "ensure-parsers", workerPayload(t, map[string]any{"storeRoot": store, "cacheRoot": cache, "parsers": "not-a-parser-map"}), "decode ensure-parsers request"},
		{"malformed execution request", "ensure-execution", workerPayload(t, map[string]any{"storeRoot": store, "cacheRoot": cache, "runtimes": "not-a-runtime-map"}), "decode ensure-execution request"},
		{"unknown operation", "future-operation", json.RawMessage(`{}`), `unknown provision operation "future-operation"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before, err := os.ReadDir(store)
			if err != nil {
				t.Fatal(err)
			}
			_, err = handleProvisionOperation(context.Background(), test.operation, test.payload)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("handleProvisionOperation() error = %v, want containing %q", err, test.contains)
			}
			after, readErr := os.ReadDir(store)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(after) != len(before) {
				t.Fatalf("rejected request mutated store: before=%v after=%v", before, after)
			}
		})
	}
}

func TestHandleProvisionOperationRejectsInvalidInstallAppActionsBeforeLease(t *testing.T) {
	for _, payload := range []string{`{`, `{"action":"unknown"}`} {
		_, err := handleProvisionOperation(context.Background(), "install-apps", json.RawMessage(payload))
		if err == nil {
			t.Fatalf("payload %q accepted", payload)
		}
	}
}

func TestHandleProvisionOperationRejectsBundleAndRuntimeAppMissingInputs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	store, cache := env.GetStorePath(), env.GetCachePath()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{
		"storeRoot": store, "cacheRoot": cache, "apps": map[string]any{},
		"bundles": map[string]any{}, "runtimeDefinitions": json.RawMessage(`{}`),
	}
	for _, tc := range []struct {
		name   string
		action string
		want   string
	}{
		{"bundle name count", "install-bundle", "requires one bundle name"},
		{"runtime app absent", "install-runtime-app", `runtime app "missing" is missing`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := make(map[string]any, len(base)+2)
			maps.Copy(request, base)
			request["action"] = tc.action
			request["appName"] = "missing"
			if tc.action == "install-bundle" {
				request["names"] = []string{}
			}
			payload := workerPayload(t, request)
			_, err := handleProvisionOperation(context.Background(), "install-apps", payload)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestCheckWorkerRootsRejectsDifferentStoreAndCache(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	for _, tc := range []struct {
		store, cache string
		want         string
	}{
		{filepath.Join(root, "elsewhere"), env.GetCachePath(), "worker store root mismatch"},
		{env.GetStorePath(), filepath.Join(root, "elsewhere"), "worker cache root mismatch"},
	} {
		err := checkWorkerRoots(tc.store, tc.cache)
		if err == nil || err.Error() != tc.want {
			t.Errorf("checkWorkerRoots(%q, %q) = %v, want %q", tc.store, tc.cache, err, tc.want)
		}
	}
	if err := checkWorkerRoots("", ""); err != nil {
		t.Fatalf("empty roots: %v", err)
	}
}

func TestDecodeRuntimeDefinitionsRejectsMalformedJSON(t *testing.T) {
	_, err := decodeRuntimeDefinitions(json.RawMessage(`{"broken":`))
	if err == nil || !strings.Contains(err.Error(), "decode runtime definitions") {
		t.Fatalf("decodeRuntimeDefinitions() error = %v", err)
	}
	got, err := decodeRuntimeDefinitions(nil)
	if err != nil || got == nil {
		t.Fatalf("empty runtime definitions = %#v, %v; want initialized map", got, err)
	}
}

func workerPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestHandleProvisionOperationEnsuresSystemRuntimeExecution(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	req := runtimemanager.EnsureExecutionRequest{
		StoreRoot: env.GetStorePath(),
		CacheRoot: env.GetCachePath(),
		Runtimes: config.MapOfRuntimes{
			"host-go": {
				Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem,
				System: &config.RuntimeConfigSystem{Command: executable, SystemVersion: "test"},
				Go:     &config.RuntimeConfigGo{GoVersion: "test"},
			},
		},
		Name: "host-go",
	}
	var got runtimemanager.RuntimeExecution
	result, err := handleProvisionOperation(context.Background(), "ensure-execution", workerPayload(t, req))
	if err != nil {
		t.Fatalf("handleProvisionOperation() error = %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Executable != executable || got.Kind != config.RuntimeKindGo || got.Status != runtimemanager.RuntimeStatusInstalled {
		t.Fatalf("execution = %#v, want installed host runtime at %q", got, executable)
	}
	if got.Env["GOCACHE"] == "" || got.Env["GOMODCACHE"] == "" || got.Env["GOPATH"] == "" {
		t.Fatalf("runtime-owned Go cache environment missing: %#v", got.Env)
	}
}

func TestHandleProvisionOperationInstallsNoSystemRuntimeAsSideEffect(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	req := runtimemanager.InstallRuntimesRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(),
		Runtimes: config.MapOfRuntimes{"host-go": {
			Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: executable},
			Go:     &config.RuntimeConfigGo{GoVersion: "test"},
		}},
		Names: []string{"host-go"}, Concurrency: 1,
	}
	result, err := handleProvisionOperation(context.Background(), "install-runtimes", workerPayload(t, req))
	if err != nil {
		t.Fatalf("install-runtimes error = %v", err)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("install-runtimes result is not transportable: %v", err)
	}
	if _, err := os.Stat(env.GetStorePath()); !os.IsNotExist(err) {
		t.Fatalf("system runtime install store path stat = %v, want no store directory", err)
	}
	var stats struct {
		AlreadyCached []string `json:"AlreadyCached"`
	}
	if err := json.Unmarshal(workerPayload(t, result), &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.AlreadyCached) != 1 || stats.AlreadyCached[0] != "host-go" {
		t.Fatalf("install stats = %#v, want system runtime recorded as already cached", stats)
	}
}

func TestHandleProvisionOperationEnsuresPinnedParser(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	module := []byte("verified parser module bytes")
	hash := digest.SHA256Of(module).String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(module)
	}))
	defer server.Close()
	request := parsermanager.EnsureRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(),
		Parsers: config.MapOfParsers{"fixture": {URL: server.URL, Hash: hash}}, Names: []string{"fixture"},
	}
	result, err := handleProvisionOperation(context.Background(), "ensure-parsers", workerPayload(t, request))
	if err != nil {
		t.Fatalf("ensure-parsers error = %v", err)
	}
	var got parsermanager.EnsureResult
	if err := json.Unmarshal(workerPayload(t, result), &got); err != nil {
		t.Fatal(err)
	}
	path := got.Paths["fixture"]
	if path == "" {
		t.Fatalf("ensure-parsers result has no fixture path: %#v", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != string(module) {
		t.Fatalf("verified parser contents = %q, err = %v", contents, err)
	}
}

func TestHandleProvisionOperationPrepareAppsEmptySelection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := binmanager.PrepareAppsRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(),
		Apps:    binmanager.MapOfApps{"probe": {Shell: &binmanager.AppConfigShell{Name: executable}}},
		Bundles: binmanager.MapOfBundles{}, RuntimeDefinitions: json.RawMessage(`{}`), Names: []string{"probe"},
	}
	result, err := handleProvisionOperation(context.Background(), "prepare-apps", workerPayload(t, request))
	if err != nil {
		t.Fatalf("prepare-apps empty selection error = %v", err)
	}
	if _, ok := result.(struct{}); !ok {
		t.Fatalf("prepare-apps result = %#v, want empty success result", result)
	}
}

func TestHandleProvisionOperationResolvesConfiguredShellApp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	request := binmanager.ProvisionRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(), Action: "get-command-info",
		Apps:    binmanager.MapOfApps{"probe": {Shell: &binmanager.AppConfigShell{Name: "sh", Args: []string{"-c", "printf ready"}}}},
		Bundles: binmanager.MapOfBundles{}, RuntimeDefinitions: json.RawMessage(`{}`), AppName: "probe",
	}
	result, err := handleProvisionOperation(context.Background(), "install-apps", workerPayload(t, request))
	if err != nil {
		t.Fatalf("install-apps get-command-info error = %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(encoded, &info); err != nil {
		t.Fatal(err)
	}
	if info.Command == "" || len(info.Args) == 0 {
		t.Fatalf("command info = %#v, want resolved shell command and args", info)
	}
}

func TestProvisionWorkerStartupRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows worker startup is not available")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	worker, err := provision.StartWorker(context.Background())
	if err != nil {
		t.Fatalf("StartWorker() error = %v", err)
	}
	t.Cleanup(func() { _ = worker.Close() })
	request := runtimemanager.InstallRuntimesRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(),
		Runtimes: config.MapOfRuntimes{}, Names: []string{}, Concurrency: 1,
	}
	var result runtimemanager.RuntimeInstallStats
	if err := worker.Client.Call(context.Background(), "install-runtimes", request, &result); err != nil {
		t.Fatalf("worker install-runtimes call error = %v", err)
	}
	if len(result.Downloaded) != 0 || len(result.AlreadyCached) != 0 || len(result.Skipped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("empty runtime install result = %#v", result)
	}
	if err := worker.Close(); err != nil {
		t.Fatalf("worker shutdown = %v", err)
	}
}

func TestHandleProvisionOperationAcceptsEmptyProvisionPlans(t *testing.T) {
	for _, action := range []string{"install", "install-concurrency", "ensure-tools", "install-bundles", "get-command-info-install-only", "ensure-prepared"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			apps := binmanager.MapOfApps{}
			appName := ""
			if action == "get-command-info-install-only" || action == "ensure-prepared" {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				apps["probe"] = binmanager.App{Shell: &binmanager.AppConfigShell{Name: executable}}
				appName = "probe"
			}
			request := binmanager.ProvisionRequest{
				StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(), Action: action,
				Apps: apps, Bundles: binmanager.MapOfBundles{}, RuntimeDefinitions: json.RawMessage(`{}`),
				Names: []string{}, AppName: appName, Concurrency: 1,
			}
			result, err := handleProvisionOperation(context.Background(), "install-apps", workerPayload(t, request))
			if err != nil {
				t.Fatalf("action %q error = %v", action, err)
			}
			if _, err := json.Marshal(result); err != nil {
				t.Fatalf("action %q result is not transportable: %v", action, err)
			}
		})
	}
}

func TestHandleProvisionOperationGeneratesLocalGoLock(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("GOPROXY", "off")
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	request := runtimemanager.GenerateLockRequest{
		StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(),
		Runtimes: config.MapOfRuntimes{"go": {
			Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "go", SystemVersion: "test"},
		}},
		AppName: "local-tool", App: binmanager.App{
			Go:    &binmanager.AppConfigGo{PackageName: "example.invalid/local/tool", Version: "v1.0.0", Runtime: "go"},
			Files: map[string]string{"go.mod": "module example.invalid/local\n\ngo 1.20\n"},
		},
	}
	result, err := handleProvisionOperation(context.Background(), "generate-lock", workerPayload(t, request))
	if err != nil {
		t.Fatalf("generate-lock error = %v", err)
	}
	var lock runtimemanager.GenerateLockResult
	if err := json.Unmarshal(workerPayload(t, result), &lock); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lock.Content, "module example.invalid/local") {
		t.Fatalf("generated Go lock = %q, want local fixture module", lock.Content)
	}
	if entries, err := os.ReadDir(filepath.Join(env.GetStorePath(), ".lockfiles")); err == nil && len(entries) != 0 {
		t.Fatalf("private lock workspace leaked: %v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestProvisionWorkerContextCancellationWaitsForShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows worker startup is not available")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	worker, err := provision.StartWorker(ctx)
	if err != nil {
		cancel()
		t.Fatalf("StartWorker() error = %v", err)
	}
	cancel()
	if err := worker.Close(); err != nil {
		t.Fatalf("worker shutdown after cancellation = %v", err)
	}
}
