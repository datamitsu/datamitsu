package runtimemanager

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

func TestPythonMissingManagedHostArtifactFailsBeforeMaterialization(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	runtime := testUVRuntimeConfig(t, "unused", "3.14.6")
	runtime.Mode, runtime.System = config.RuntimeModeManaged, nil
	runtime.Managed = &config.RuntimeConfigManaged{Binaries: binmanager.MapOfBinaries{}}
	manager := New(config.MapOfRuntimes{"python": runtime})
	if _, err := manager.ResolvePython("python"); err == nil {
		t.Fatal("read-only Python resolution accepted an unavailable managed artifact")
	}
	if _, err := manager.EnsurePython(context.Background(), "python"); err == nil {
		t.Fatal("Python provisioning accepted an unavailable managed artifact")
	}
	if _, err := os.Stat(env.GetStorePath()); !os.IsNotExist(err) {
		t.Fatalf("unavailable acquisition tool materialized store content: %v", err)
	}
}

func TestPythonRepairRemovalFailurePreservesPayloadAndReleasesLease(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "")
	runtime := testUVRuntimeConfig(t, os.Args[0], "3.14.6")
	manager := New(config.MapOfRuntimes{"python": runtime})
	identity, err := manager.runtimeDataIdentity("python", runtime)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(env.GetRuntimeDataPath("python", identity), "python")
	payload := filepath.Join(root, "bin", "python")
	if err := os.MkdirAll(filepath.Dir(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("unmarked payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager.removeAllFunc = func(path string) error {
		if path == root {
			return fs.ErrPermission
		}
		return ForceRemoveAll(path)
	}
	if _, err := manager.EnsurePython(context.Background(), "python"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Python repair ignored invalid-payload removal failure: %v", err)
	}
	if data, err := os.ReadFile(payload); err != nil || string(data) != "unmarked payload" {
		t.Fatalf("failed repair changed the old payload: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, pythonReadyFilename)); !os.IsNotExist(err) {
		t.Fatalf("failed repair published readiness: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := storelock.Clear(ctx, env.GetStorePath())
	if err != nil {
		t.Fatalf("failed repair retained provisioning ownership: %v", err)
	}
	release()
}
