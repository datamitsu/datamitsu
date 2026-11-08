package binmanager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

func TestFailedBundleStagingNeverPublishesReadiness(t *testing.T) {
	for filename, contents := range map[string]string{
		"../outside":                "must stay inside",
		bundleReadyMarker + "/keep": "marker conflict",
	} {
		t.Run(filename, func(t *testing.T) {
			files := map[string]string{filename: contents}
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			manager := New(nil, nil, nil)
			manager.mapOfBundles = MapOfBundles{"probe": {Files: files}}
			target, err := manager.ComputeBundlePath("probe")
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.InstallBundleByName(context.Background(), "probe"); err == nil {
				t.Fatal("invalid bundle staging published successfully")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("failed staging published a bundle target: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(target))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".bundle-staging-") {
					t.Fatalf("failed bundle left staging data: %s", entry.Name())
				}
			}
		})
	}
}

func TestBundleTargetWaitCancellationReleasesStoreLease(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	manager := New(nil, nil, nil)
	manager.mapOfBundles = MapOfBundles{"probe": {Files: map[string]string{"config": "value"}}}
	target, err := manager.ComputeBundlePath("probe")
	if err != nil {
		t.Fatal(err)
	}
	release, err := storelock.Lock(context.Background(), env.GetStorePath(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := manager.InstallBundleByName(ctx, "probe"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bundle acquisition did not cancel at its held target: %v", err)
	}
	release()
	clearCtx, cancelClear := context.WithTimeout(context.Background(), time.Second)
	defer cancelClear()
	releaseClear, err := storelock.Clear(clearCtx, env.GetStorePath())
	if err != nil {
		t.Fatalf("canceled bundle waiter leaked its store lease: %v", err)
	}
	releaseClear()
}

func TestBundlePublishConflictPreservesNewDestination(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "")
	archive := cpTarGzBytes(t, map[string]string{"tool": "payload"})
	var target string
	configured := make(chan struct{})
	conflictErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-configured
		err := os.MkdirAll(target, 0o755)
		if err == nil {
			err = os.WriteFile(filepath.Join(target, "keep"), []byte("other payload"), 0o600)
		}
		conflictErr <- err
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	bundle := &Bundle{Archives: map[string]*ArchiveSpec{"payload": {URL: server.URL, Hash: digest.SHA256Of(archive).String(), Format: BinContentTypeTarGz}}}
	manager := New(nil, nil, nil)
	manager.mapOfBundles = MapOfBundles{"probe": bundle}
	var err error
	target, err = manager.ComputeBundlePath("probe")
	if err != nil {
		t.Fatal(err)
	}
	close(configured)
	if err := manager.InstallBundleByName(context.Background(), "probe"); err == nil {
		t.Fatal("publication replaced a nonempty destination created during download")
	}
	select {
	case err := <-conflictErr:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("bundle failed before the destination conflict was exercised")
	}
	if got, err := os.ReadFile(filepath.Join(target, "keep")); err != nil || string(got) != "other payload" {
		t.Fatalf("publication damaged the competing destination: %q, %v", got, err)
	}
	if bundleReady(target, bundle) {
		t.Fatal("failed publication marked the competing destination ready")
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".bundle-staging-") {
			t.Fatalf("failed publication left staging data: %s", entry.Name())
		}
	}
}
