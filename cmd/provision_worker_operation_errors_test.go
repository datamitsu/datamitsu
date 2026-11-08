package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ocibundle"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

func TestHandleProvisionOperationRuntimeAppPrerequisiteErrorsDoNotMutateApps(t *testing.T) {
	for _, tc := range []struct {
		kind string
		app  binmanager.App
	}{
		{"uv", binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "example", Version: "1", Runtime: "missing"}}},
		{"node", binmanager.App{Node: &binmanager.AppConfigNode{PackageName: "example", Version: "1", BinPath: "bin/example", Runtime: "missing"}}},
		{"bun", binmanager.App{Bun: &binmanager.AppConfigBun{PackageName: "example", Version: "1", BinPath: "bin/example", Runtime: "missing"}}},
		{"go", binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "example", Version: "1", Runtime: "missing"}}},
		{"jvm", binmanager.App{Jvm: &binmanager.AppConfigJVM{JarURL: "https://example.invalid/tool.jar", JarHash: "sha256:" + strings.Repeat("a", 64), Version: "1", Runtime: "missing"}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			store, cache := workerTestRoots(t)
			request := binmanager.ProvisionRequest{
				StoreRoot: store, CacheRoot: cache, Apps: binmanager.MapOfApps{"probe": tc.app},
				RuntimeDefinitions: json.RawMessage(`{}`), Action: "install-runtime-app", AppName: "probe",
			}
			_, err := handleProvisionOperation(context.Background(), "install-apps", workerPayload(t, request))
			if err == nil || !strings.Contains(err.Error(), `runtime "missing" referenced by app not found`) {
				t.Fatalf("error = %v, want missing runtime prerequisite classification", err)
			}
			assertNoAppMutation(t, store)
		})
	}
}

func TestHandleProvisionOperationRuntimeAppDomainErrorsDoNotMutateApps(t *testing.T) {
	store, cache := workerTestRoots(t)
	base := binmanager.ProvisionRequest{StoreRoot: store, CacheRoot: cache, Apps: binmanager.MapOfApps{}, RuntimeDefinitions: json.RawMessage(`{}`), Action: "install-runtime-app", AppName: "absent"}
	_, err := handleProvisionOperation(context.Background(), "install-apps", workerPayload(t, base))
	if err == nil || !strings.Contains(err.Error(), `runtime app "absent" is missing`) {
		t.Fatalf("missing app error = %v", err)
	}
	base.Apps["unsupported"] = binmanager.App{Shell: &binmanager.AppConfigShell{Name: "true"}}
	base.AppName = "unsupported"
	_, err = handleProvisionOperation(context.Background(), "install-apps", workerPayload(t, base))
	if err == nil || !strings.Contains(err.Error(), "not a runtime-managed app") {
		t.Fatalf("unsupported app error = %v", err)
	}
	assertNoAppMutation(t, store)
}

func TestHandleProvisionOperationRejectsWellFormedInvalidDomainRequestsBeforeStoreWrites(t *testing.T) {
	store, cache := workerTestRoots(t)
	tests := []struct {
		name, operation, want string
		payload               any
	}{
		{"generate lock app", "generate-lock", "does not support lockfile generation", runtimemanager.GenerateLockRequest{StoreRoot: store, CacheRoot: cache, AppName: "probe"}},
		{"prepare missing app", "prepare-apps", "not found in registry", binmanager.PrepareAppsRequest{StoreRoot: store, CacheRoot: cache, Apps: binmanager.MapOfApps{}, Names: []string{"absent"}}},
		{"parser missing module", "ensure-parsers", "is not declared", parsermanager.EnsureRequest{StoreRoot: store, CacheRoot: cache, Parsers: config.MapOfParsers{}, Names: []string{"absent"}}},
		{"execution missing runtime", "ensure-execution", "unknown runtime", runtimemanager.EnsureExecutionRequest{StoreRoot: store, CacheRoot: cache, Runtimes: config.MapOfRuntimes{}, Name: "absent"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handleProvisionOperation(context.Background(), tc.operation, workerPayload(t, tc.payload))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("error = %v, want classification containing %q", err, tc.want)
			}
			assertNoAppMutation(t, store)
		})
	}
	t.Run("seed missing source", func(t *testing.T) {
		result, err := handleProvisionOperation(context.Background(), "seed-oci", workerPayload(t, ocibundle.SeedRequest{StoreRoot: store, CacheRoot: cache}))
		if err != nil {
			t.Fatalf("seed-oci transport error = %v", err)
		}
		seed, ok := result.(ocibundle.SeedResult)
		if !ok || seed.Error == "" {
			t.Fatalf("seed-oci result = %#v, want classified seed failure", result)
		}
		assertNoAppMutation(t, store)
	})
}

func TestHandleProvisionOperationRejectsInvalidRootsAndCanceledLease(t *testing.T) {
	store, cache := workerTestRoots(t)
	request := runtimemanager.EnsureExecutionRequest{StoreRoot: store, CacheRoot: cache, Runtimes: config.MapOfRuntimes{}, Name: "missing"}
	request.StoreRoot = filepath.Join(filepath.Dir(store), "outside-store")
	_, err := handleProvisionOperation(context.Background(), "ensure-execution", workerPayload(t, request))
	if err == nil || !strings.Contains(err.Error(), "worker store root mismatch") {
		t.Fatalf("bad store root error = %v", err)
	}
	request.StoreRoot = store
	request.CacheRoot = filepath.Join(filepath.Dir(cache), "outside-cache")
	_, err = handleProvisionOperation(context.Background(), "ensure-execution", workerPayload(t, request))
	if err == nil || !strings.Contains(err.Error(), "worker cache root mismatch") {
		t.Fatalf("bad cache root error = %v", err)
	}
	request.CacheRoot = cache
	release, err := storelock.Clear(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = handleProvisionOperation(ctx, "ensure-execution", workerPayload(t, request))
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("canceled lease error = %v", err)
	}
	assertNoAppMutation(t, store)
}

func workerTestRoots(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	store, cache := env.GetStorePath(), env.GetCachePath()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	return store, cache
}

func assertNoAppMutation(t *testing.T, store string) {
	t.Helper()
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == ".apps" || entry.Name() == ".prepared" || entry.Name() == ".runtime-data" {
			t.Fatalf("failed operation mutated app/runtime store subtree %q", entry.Name())
		}
	}
}
