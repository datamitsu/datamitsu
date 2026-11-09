package installstate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestInvalidTargetCannotCreateStatusOrTouchOutsidePayload(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("preserve outside payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, release, err := Acquire(context.Background(), filepath.Join(base, "store"), "../outside", "invalid"); err == nil {
		release()
		t.Fatal("status acquisition accepted a target outside the store")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve outside payload" {
		t.Fatalf("invalid status target changed outside payload: %q, %v", data, err)
	}
	if _, err := os.Stat(env.GetCachePath()); !os.IsNotExist(err) {
		t.Fatalf("invalid target created cache-side status or lock metadata: %v", err)
	}
}
