package storelock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestLeaseStorageConflictsNeverAdmitOwnerOrLeakGate(t *testing.T) {
	for _, exclusive := range []bool{false, true} {
		for _, component := range []string{"gate", "activity"} {
			name := "shared/" + component
			if exclusive {
				name = "exclusive/" + component
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
				store := t.TempDir()
				gate, activity, err := leaseFiles(store)
				if err != nil {
					t.Fatal(err)
				}
				conflict := gate
				if component == "activity" {
					conflict = activity
				}
				if err := os.Mkdir(conflict, 0o700); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if exclusive {
					if release, err := Clear(ctx, store); err == nil {
						release()
						t.Fatal("exclusive ownership admitted through invalid lock storage")
					}
				} else if _, release, err := WithStore(ctx, store); err == nil {
					release()
					t.Fatal("shared ownership admitted through invalid lock storage")
				}
				if err := os.Remove(conflict); err != nil {
					t.Fatal(err)
				}
				release, err := Clear(ctx, store)
				if err != nil {
					t.Fatalf("failed acquisition leaked the gate: %v", err)
				}
				release()
			})
		}
	}
}

func TestInvalidTargetLockStorageDoesNotReportContention(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	store := t.TempDir()
	identity, err := TargetIdentity(store, "apps/probe")
	if err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(env.GetCachePath(), "store-locks", identity+".lock")
	if err := os.MkdirAll(conflict, 0o700); err != nil {
		t.Fatal(err)
	}
	waited := false
	if release, err := LockWithWait(context.Background(), store, "apps/probe", func() { waited = true }); err == nil {
		release()
		t.Fatal("target lock admitted ownership through a directory")
	}
	if waited {
		t.Fatal("invalid lock storage was reported as another installation owner")
	}
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	release, err := Lock(context.Background(), store, "apps/probe")
	if err != nil {
		t.Fatalf("target did not recover after conflict removal: %v", err)
	}
	release()
}
