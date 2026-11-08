package binmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

func TestInstalledSourceRejectsUnavailableOwnershipAndUnhealthyPayload(t *testing.T) {
	for _, scenario := range []string{"unknown app", "missing payload", "directory payload", "escaped payload", "unavailable lease", "canceled target wait"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			payload := []byte("preserved")
			info := BinaryOsArchInfo{URL: "https://example.invalid/tool", Hash: digest.SHA256Of(payload).String(), ContentType: BinContentTypeBinary}
			if scenario == "directory payload" {
				command := "bin/tool"
				info.ExtractDir, info.BinaryPath, info.ContentType = true, &command, BinContentTypeTarGz
			}
			manager := New(MapOfApps{"tool": {Binary: &AppConfigBinary{Binaries: cpHostBinaries(t, info)}}}, nil, nil)
			installPath, err := manager.getInstallPath("tool")
			if err != nil {
				t.Fatal(err)
			}
			name := "tool"
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var outside string
			switch scenario {
			case "unknown app":
				name = "absent"
			case "directory payload":
				if err := os.MkdirAll(installPath, 0o755); err != nil {
					t.Fatal(err)
				}
			case "escaped payload":
				outside = filepath.Join(t.TempDir(), "preserve")
				if err := os.WriteFile(outside, payload, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, installPath); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "unavailable lease":
				if err := os.WriteFile(env.GetCachePath(), []byte("preserve cache"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "canceled target wait":
				release, err := storelock.Lock(ctx, env.GetStorePath(), installPath)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 30*time.Millisecond)
				defer stop()
			}
			used := false
			err = manager.WithInstalledBinarySource(ctx, name, func(string) error { used = true; return nil })
			if err == nil || used {
				t.Fatalf("unavailable source reached runtime copying: used=%v, error=%v", used, err)
			}
			if scenario == "canceled target wait" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("source waiter did not cancel at its held target: %v", err)
			}
			if outside != "" {
				if got, err := os.ReadFile(outside); err != nil || string(got) != string(payload) {
					t.Fatalf("rejected source changed outside payload: %q, %v", got, err)
				}
			}
		})
	}
}
