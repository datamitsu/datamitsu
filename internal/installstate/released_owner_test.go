package installstate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestReleasedOwnerCannotOverwriteReplacementProgress(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	store := filepath.Join(t.TempDir(), "store")
	oldContext, releaseOld, err := Acquire(context.Background(), store, "apps/probe", "old owner")
	if err != nil {
		t.Fatal(err)
	}
	releaseOld()
	newContext, releaseNew, err := Acquire(context.Background(), store, "apps/probe", "new owner")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseNew()
	Progress(newContext, PhaseDownload, 3, 10, "bytes")
	directory := filepath.Join(env.GetCachePath(), "store-locks", "status")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("replacement owner status files = %v, error = %v", entries, err)
	}
	path := filepath.Join(directory, entries[0].Name())
	readRecord := func() status {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var record status
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	before := readRecord()
	Progress(oldContext, PhasePublish, 99, 100, "packages")
	after := readRecord()
	if after.Owner != before.Owner || after.Label != "new owner" || after.Phase != "download" || after.Completed == nil || *after.Completed != 3 {
		t.Fatalf("released owner overwrote replacement progress: before=%+v after=%+v", before, after)
	}
}
