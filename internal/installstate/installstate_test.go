package installstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestAcquireProgressPublishesStatus(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	store := filepath.Join(t.TempDir(), "store")
	ctx, release, err := Acquire(context.Background(), store, "apps/example", "example")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	Progress(ctx, "download", 2, 5, "bytes")
	path := filepath.Join(env.GetCachePath(), "store-locks", "status")
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("status entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(path, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var got status
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != schema || got.Label != "example" || got.Phase != "download" || got.Completed == nil || *got.Completed != 2 || got.Total == nil || *got.Total != 5 || got.Unit != "bytes" {
		t.Fatalf("unexpected status: %+v", got)
	}
}

func TestAcquireWaitsAndHonorsCancellation(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	store := filepath.Join(t.TempDir(), "store")
	_, release, err := Acquire(context.Background(), store, "apps/example", "first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, _, err := Acquire(ctx, store, "apps/example", "second"); err == nil {
		t.Fatal("expected canceled wait")
	}
	release()
	ctx, done, err := Acquire(context.Background(), store, "apps/example", "second")
	if err != nil {
		t.Fatal(err)
	}
	done()
	Progress(ctx, "ignored", 0, 0, "")
}

func TestUnavailableStatusStoragePreservesExclusiveOwnership(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	dir := filepath.Join(env.GetCachePath(), "store-locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(t.TempDir(), "store")
	owner, release, err := Acquire(context.Background(), store, ".runtimes/probe/0123456789abcdef0123456789abcdef", "probe")
	if err != nil {
		t.Fatalf("advisory status failure blocked ownership: %v", err)
	}
	defer release()
	Progress(owner, PhaseDownload, 1, 10, "bytes")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := Acquire(ctx, store, ".runtimes/probe/0123456789abcdef0123456789abcdef", "waiter"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("status failure weakened exclusive target ownership: %v", err)
	}
	release()
	_, releaseNext, err := Acquire(context.Background(), store, ".runtimes/probe/0123456789abcdef0123456789abcdef", "next")
	if err != nil {
		t.Fatalf("next owner failed after release: %v", err)
	}
	releaseNext()
}

func TestNestedAcquirePropagatesProgressToEveryOwner(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	store := filepath.Join(t.TempDir(), "store")
	parent, releaseParent, err := Acquire(context.Background(), store, ".runtimes/node/identity", "node")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseParent()
	child, releaseChild, err := Acquire(parent, store, ".bin/pnpm/identity", "pnpm")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseChild()
	Progress(child, PhaseDownload, 4, 10, "bytes")
	statusDir := filepath.Join(env.GetCachePath(), "store-locks", "status")
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("nested acquisitions published %d statuses, want 2", len(entries))
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(statusDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var got status
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Phase != string(PhaseDownload) || got.Completed == nil || *got.Completed != 4 || got.Total == nil || *got.Total != 10 || got.Unit != "bytes" {
			t.Errorf("nested progress did not reach owner %q: %+v", got.Label, got)
		}
	}
}

func TestValidPhaseRecognizesOnlyInstallLifecycleStages(t *testing.T) {
	for _, phase := range []string{"download", "verify", "extract", "install", "publish"} {
		if !validPhase(phase) {
			t.Errorf("validPhase(%q) = false", phase)
		}
	}
	for _, phase := range []string{"", "complete", "invalid"} {
		if validPhase(phase) {
			t.Errorf("validPhase(%q) = true", phase)
		}
	}
}
