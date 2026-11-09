package storelock

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTargetRejectsInvalidOwnersAndExistingEscapes(t *testing.T) {
	base := t.TempDir()
	for _, tc := range []struct {
		name   string
		root   string
		target string
	}{
		{"relative-root", "relative", "target"},
		{"absolute-target", filepath.Join(base, "store"), filepath.Join(base, "outside")},
		{"dot-target", filepath.Join(base, "store"), "."},
		{"parent-target", filepath.Join(base, "store"), filepath.Join("..", "outside")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateTarget(tc.root, tc.target); err == nil {
				t.Fatalf("ValidateTarget(%q,%q) succeeded", tc.root, tc.target)
			}
		})
	}
	root := filepath.Join(base, "store")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ValidateTarget(root, filepath.Join("escape", "target")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink escape error = %v", err)
	}
}

func TestWithStoreRejectsUnusableLockDirectory(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(cache, "base"))
	lockParent := filepath.Join(cache, "base", "cache", "store-locks")
	if err := os.MkdirAll(filepath.Dir(lockParent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockParent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, release, err := WithStore(context.Background(), filepath.Join(base, "store")); err == nil {
		release()
		t.Fatal("lease accepted a file as lock directory")
	}
}
