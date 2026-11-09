package binmanager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

const bundleRaceHelper = "BINMANAGER_BUNDLE_RACE_HELPER"

func TestBundleInstallRaceHelper(t *testing.T) {
	if os.Getenv(bundleRaceHelper) != "1" {
		t.Skip("bundle race subprocess helper")
	}
	ready := os.Getenv("BINMANAGER_BUNDLE_RACE_READY")
	gate := os.Getenv("BINMANAGER_BUNDLE_RACE_GATE")
	result := os.Getenv("BINMANAGER_BUNDLE_RACE_RESULT")
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	bm := New(MapOfApps{}, MapOfBundles{"probe": {Version: "v1", Files: map[string]string{"payload": "complete"}}}, nil)
	stats, err := bm.InstallBundles(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	state := "installed"
	if len(stats.AlreadyCached) == 1 {
		state = "cached"
	}
	if err := os.WriteFile(result, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentProcessesRepairPartialBundleOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	bundles := MapOfBundles{"probe": {Version: "v1", Files: map[string]string{"payload": "complete"}}}
	bm := New(MapOfApps{}, bundles, nil)
	target, err := bm.ComputeBundlePath("probe")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "payload"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(root, "release-bundle-install")
	results := make([]string, 2)
	commands := make([]*exec.Cmd, 2)
	for i := range commands {
		ready := filepath.Join(root, fmt.Sprintf("ready-%d", i))
		results[i] = filepath.Join(root, fmt.Sprintf("result-%d", i))
		cmd := exec.Command(os.Args[0], "-test.run=^TestBundleInstallRaceHelper$")
		cmd.Env = append(os.Environ(), bundleRaceHelper+"=1", "BINMANAGER_BUNDLE_RACE_READY="+ready, "BINMANAGER_BUNDLE_RACE_GATE="+gate, "BINMANAGER_BUNDLE_RACE_RESULT="+results[i])
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		commands[i] = cmd
	}
	waitReadyFiles(t, root, 2)
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("bundle helper failed: %v", err)
		}
	}
	states := make([]string, 2)
	for i, result := range results {
		data, err := os.ReadFile(result)
		if err != nil {
			t.Fatal(err)
		}
		states[i] = string(data)
	}
	if strings.Count(strings.Join(states, ","), "installed") != 1 || strings.Count(strings.Join(states, ","), "cached") != 1 {
		t.Fatalf("concurrent bundle results = %v, want one install and one locked cache hit", states)
	}
	if !bundleReady(target, bundles["probe"]) {
		t.Fatal("concurrent repair did not publish a ready bundle")
	}
	payload, err := os.ReadFile(filepath.Join(target, "payload"))
	if err != nil || string(payload) != "complete" {
		t.Fatalf("published payload = %q, err %v", payload, err)
	}
}

func TestConcurrentClearWaitsForBundleProvisioning(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", root)
	bundles := MapOfBundles{"probe": {Version: "v1", Files: map[string]string{"payload": "complete"}}}
	bm := New(MapOfApps{}, bundles, nil)
	clearRelease, err := storelock.Clear(context.Background(), env.GetStorePath())
	if err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(root, "release-bundle-install")
	ready := filepath.Join(root, "bundle-helper-ready")
	result := filepath.Join(root, "bundle-helper-result")
	cmd := exec.Command(os.Args[0], "-test.run=^TestBundleInstallRaceHelper$")
	cmd.Env = append(os.Environ(), bundleRaceHelper+"=1", "BINMANAGER_BUNDLE_RACE_READY="+ready, "BINMANAGER_BUNDLE_RACE_GATE="+gate, "BINMANAGER_BUNDLE_RACE_RESULT="+result)
	if err := cmd.Start(); err != nil {
		clearRelease()
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	waitReadyPath(t, ready)
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		clearRelease()
		t.Fatal(err)
	}
	// The explicit child barrier is released while the exclusive clear lease is
	// held; publication is possible only after the lease is released.
	if _, err := os.Stat(result); !os.IsNotExist(err) {
		clearRelease()
		t.Fatalf("bundle result appeared while clear held the store: %v", err)
	}
	clearRelease()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("bundle helper failed after clear release: %v", err)
	}
	if !bundleReady(mustBundlePath(t, bm), bundles["probe"]) {
		t.Fatal("bundle was not installed after clear released")
	}
}

func waitReadyFiles(t *testing.T, root string, count int) {
	t.Helper()
	for i := range count {
		waitReadyPath(t, filepath.Join(root, fmt.Sprintf("ready-%d", i)))
	}
}

func waitReadyPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not reach barrier: %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustBundlePath(t *testing.T, bm *BinManager) string {
	t.Helper()
	path, err := bm.ComputeBundlePath("probe")
	if err != nil {
		t.Fatal(err)
	}
	return path
}
