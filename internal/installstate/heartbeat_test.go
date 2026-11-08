package installstate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestHeartbeatPublishesLatestThrottledPackageCounters(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	ctx, release, err := Acquire(context.Background(), filepath.Join(t.TempDir(), "store"), "apps/probe", "probe")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	directory := filepath.Join(env.GetCachePath(), "store-locks", "status")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("owner status files = %v, error = %v", entries, err)
	}
	statusPath := filepath.Join(directory, entries[0].Name())
	waitForCounters := func(completed int64) {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var latest status
		for {
			data, err := os.ReadFile(statusPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &latest); err != nil {
				t.Fatal(err)
			}
			if latest.Completed != nil && *latest.Completed == completed && latest.Total != nil && *latest.Total == 10 && latest.Unit == "packages" && latest.Phase == "install" {
				return
			}
			select {
			case <-ticker.C:
			case <-deadline.C:
				t.Fatalf("heartbeat lost throttled package progress: got %+v, want %d/10 packages", latest, completed)
			}
		}
	}
	Progress(ctx, PhaseInstall, 3, 10, "packages")
	waitForCounters(3)
	Progress(ctx, PhaseInstall, 7, 10, "packages")
	waitForCounters(7)
}
