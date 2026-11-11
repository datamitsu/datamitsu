package runtimemanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestAppReadyRequiresExactCompletionMarker(t *testing.T) {
	path := t.TempDir()
	if appReady(path) {
		t.Fatal("markerless app is ready")
	}
	marker := filepath.Join(path, appReadyMarker)
	if err := os.WriteFile(marker, []byte("runtime-data-v0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if appReady(path) {
		t.Fatal("old marker version is ready")
	}
	if err := os.WriteFile(marker, []byte(appReadyMarkerContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if !appReady(path) {
		t.Fatal("valid marker is not ready")
	}
}

func TestLockAppTargetReusesStoreLeaseAndValidatesTarget(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	storeRoot := env.GetStorePath()
	appPath := filepath.Join(storeRoot, ".apps", "uv", "tool", "identity")
	if err := os.MkdirAll(filepath.Dir(appPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := lockAppTarget(context.Background(), appPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	statusDir := filepath.Join(env.GetCachePath(), "store-locks", "status")
	entries, err := os.ReadDir(statusDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("status entries = %d, error = %v", len(entries), err)
	}
	data, err := os.ReadFile(filepath.Join(statusDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Label != "app tool" {
		t.Fatalf("app install label = %q, want %q", record.Label, "app tool")
	}
	if ctx == nil {
		t.Fatal("lockAppTarget returned a nil lease context")
	}
	if _, _, err := lockAppTarget(ctx, filepath.Join(storeRoot, "..", "outside")); err == nil {
		t.Fatal("lockAppTarget accepted a target outside the store")
	}
}

func TestMarkAppReadyReplacesMarkerAndRejectsMissingDirectory(t *testing.T) {
	path := t.TempDir()
	marker := filepath.Join(path, appReadyMarker)
	if err := os.WriteFile(marker, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := markAppReady(path); err != nil {
		t.Fatal(err)
	}
	if !appReady(path) {
		t.Fatal("markAppReady did not atomically replace the stale marker")
	}
	missing := filepath.Join(path, "missing")
	if err := markAppReady(missing); err == nil {
		t.Fatal("markAppReady succeeded for missing app directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".datamitsu-app-ready-") {
			t.Errorf("temporary app marker leaked: %s", entry.Name())
		}
	}
}
