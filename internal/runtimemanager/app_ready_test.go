package runtimemanager

import (
	"context"
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
