package cmd

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
)

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
