package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestClearProjectAppCachesPreservesStoreAndOtherRoots(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	root, otherRoot := t.TempDir(), t.TempDir()
	paths := make([]string, 0, 3)
	for _, entry := range []struct{ root, identity string }{
		{root, "version-one"}, {root, "version-two"}, {otherRoot, "version-one"},
	} {
		path, err := env.GetAppToolCachePath(entry.root, "tool", entry.identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(path, "provider")
		if err := os.WriteFile(path, []byte(entry.identity), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	store := filepath.Join(env.GetStorePath(), ".bin", "installed-tool")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("installed artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ClearProject(env.GetCachePath(), root); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths[:2] {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("project clear retained an app cache: %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(paths[2]); err != nil || string(data) != "version-one" {
		t.Fatalf("project clear changed another checkout's cache: %q, %v", data, err)
	}
	if err := ClearAll(env.GetCachePath()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths[2]); !os.IsNotExist(err) {
		t.Fatalf("clear all retained an app cache: %v", err)
	}
	if data, err := os.ReadFile(store); err != nil || string(data) != "installed artifact" {
		t.Fatalf("cache clearing changed installed store artifact: %q, %v", data, err)
	}
}
