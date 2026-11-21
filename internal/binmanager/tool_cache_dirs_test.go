package binmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateCacheNames(t *testing.T) {
	if err := ValidateCacheNames([]string{"providers", "registry_cache", "_local"}); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{
		{"default"}, {"Providers"}, {"cache1"}, {"cache-one"}, {"bad/name"}, {"bad.name"}, {"con"}, {"nul"}, {"com1"}, {"lpt9"}, {"one", "one"},
	} {
		if err := ValidateCacheNames(names); err == nil {
			t.Errorf("ValidateCacheNames(%q) succeeded", names)
		}
	}
}

func TestEnsureToolCacheSlotsLifecycle(t *testing.T) {
	managed := t.TempDir()
	parent := filepath.Join(managed, "apps", "tool")
	for range 2 {
		if err := EnsureToolCacheSlots(managed, parent, []string{"default", "providers"}); err != nil {
			t.Fatal(err)
		}
	}
	defaultSlot := filepath.Join(parent, "default")
	providersSlot := filepath.Join(parent, "providers")
	if filepath.Dir(defaultSlot) != filepath.Dir(providersSlot) || defaultSlot == providersSlot {
		t.Fatalf("cache slots are not distinct siblings: %q %q", defaultSlot, providersSlot)
	}
	for _, slot := range []string{defaultSlot, providersSlot} {
		if info, err := os.Stat(slot); err != nil || !info.IsDir() {
			t.Fatalf("slot %q was not created: info=%v err=%v", slot, info, err)
		}
	}
	if err := os.WriteFile(filepath.Join(defaultSlot, "state"), []byte("default"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providersSlot, "state"), []byte("providers"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(parent); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToolCacheSlots(managed, parent, []string{"providers"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(parent, "providers")); err != nil || !info.IsDir() {
		t.Fatalf("named cache slot was not recreated: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(parent, "default")); !os.IsNotExist(err) {
		t.Fatalf("implicit default slot was created without being selected: %v", err)
	}
}

func TestEnsureToolCacheSlotsRejectsInvalidInputsWithoutWrites(t *testing.T) {
	for _, slots := range [][]string{{"providers", "Default"}, {"default", "default"}, {"../outside"}, {"con"}, {"COM1"}} {
		parent := t.TempDir()
		managed := filepath.Join(parent, "managed")
		appCache := filepath.Join(managed, "apps", "tool")
		if err := EnsureToolCacheSlots(managed, appCache, slots); err == nil {
			t.Errorf("EnsureToolCacheSlots(%q) succeeded", slots)
		}
		if _, err := os.Lstat(managed); !os.IsNotExist(err) {
			t.Errorf("invalid declaration %q wrote into managed cache: %v", slots, err)
		}
	}
}

func TestEnsureToolCacheSlotsRejectsSymlinkAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	managed := t.TempDir()
	parent := filepath.Join(managed, "apps", "tool")
	if err := os.MkdirAll(filepath.Join(parent, "default"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("default", filepath.Join(parent, "providers")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToolCacheSlots(managed, parent, []string{"default", "providers"}); err == nil {
		t.Fatal("cache slot alias was accepted")
	}
}

func TestEnsureToolCacheSlotsRejectsNonDirectory(t *testing.T) {
	managed := t.TempDir()
	parent := filepath.Join(managed, "apps", "tool")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "providers"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToolCacheSlots(managed, parent, []string{"providers"}); err == nil {
		t.Fatal("non-directory cache slot was accepted")
	}
}

func TestEnsureToolCacheSlotsRejectsParentSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	base := t.TempDir()
	managed := filepath.Join(base, "managed")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(managed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(managed, "apps")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToolCacheSlots(managed, filepath.Join(managed, "apps", "tool"), []string{"providers"}); err == nil {
		t.Fatal("cache parent symlink was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "tool")); !os.IsNotExist(err) {
		t.Fatalf("cache creation reached outside directory: %v", err)
	}
}

func TestEnsureToolCacheSlotsConcurrentColdCreation(t *testing.T) {
	const callers = 32
	for range 8 {
		managed := t.TempDir()
		parent := filepath.Join(managed, "apps", "tool")
		start := make(chan struct{})
		results := make(chan error, callers)
		for range callers {
			go func() {
				<-start
				results <- EnsureToolCacheSlots(managed, parent, []string{"default", "providers"})
			}()
		}
		close(start)
		var firstErr error
		for range callers {
			if err := <-results; err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			t.Fatalf("concurrent cold-cache creation failed: %v", firstErr)
		}
		for _, slot := range []string{"default", "providers"} {
			if info, err := os.Lstat(filepath.Join(parent, slot)); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("invalid cache slot %q: info=%v err=%v", slot, info, err)
			}
		}
	}
}
