package runtimemanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

func TestFailedAppMarkerPublicationPreservesDestination(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, appReadyMarker)
	if err := os.Mkdir(marker, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(marker, "existing")
	if err := os.WriteFile(payload, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := markAppReady(root); err == nil {
		t.Fatal("published readiness over a nonempty directory")
	}
	if data, err := os.ReadFile(payload); err != nil || string(data) != "preserve" {
		t.Fatalf("failed publication changed existing data: %q, %v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != appReadyMarker || appReady(root) {
		t.Fatalf("failed publication left a temporary or valid marker: %v", entries)
	}
}

func TestCanceledAppTargetWaitReleasesStoreLease(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	appPath := filepath.Join(env.GetStorePath(), ".apps", "go", "probe", "identity")
	_, release, err := lockAppTarget(context.Background(), appPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := lockAppTarget(waitCtx, appPath); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled target wait = %v", err)
	}
	release()
	clearCtx, stopClear := context.WithTimeout(context.Background(), time.Second)
	defer stopClear()
	releaseClear, err := storelock.Clear(clearCtx, env.GetStorePath())
	if err != nil {
		t.Fatalf("canceled waiter leaked its store lease: %v", err)
	}
	releaseClear()
}

func TestAppProvisioningRefusesInstallerReentryBeforeMutation(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_INSTALL_CHILD", "1")
	appPath := filepath.Join(env.GetStorePath(), ".apps", "go", "probe", "identity")
	if _, _, err := lockAppTarget(context.Background(), appPath); err == nil {
		t.Fatal("installer child acquired an app target")
	}
	invoked := false
	if err := withRuntimeStoreLease(context.Background(), "probe", func(context.Context) error { invoked = true; return nil }); err == nil || invoked {
		t.Fatalf("installer child reached app provisioning: invoked=%v, error=%v", invoked, err)
	}
	if _, err := os.Stat(env.GetStorePath()); !os.IsNotExist(err) {
		t.Fatalf("rejected installer child materialized store: %v", err)
	}
}

func TestAppProvisioningRefusesUnavailableLeaseStorage(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	if err := os.WriteFile(env.GetCachePath(), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lockAppTarget(context.Background(), filepath.Join(env.GetStorePath(), ".apps", "go", "probe", "identity")); err == nil {
		t.Fatal("acquired app ownership without lease storage")
	}
	invoked := false
	if err := withRuntimeStoreLease(context.Background(), "probe", func(context.Context) error { invoked = true; return nil }); err == nil || invoked {
		t.Fatalf("provisioning ran without a store lease: invoked=%v, error=%v", invoked, err)
	}
	if data, err := os.ReadFile(env.GetCachePath()); err != nil || string(data) != "existing" {
		t.Fatalf("lease failure changed conflicting cache path: %q, %v", data, err)
	}
}
