package runtimemanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

func TestGenerateLockRejectsUnsupportedAppAndCanceledWorkspace(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(config.MapOfRuntimes{})
	if _, err := rm.GenerateLock(context.Background(), "empty", binmanager.App{}); err == nil || !strings.Contains(err.Error(), "does not support lockfile generation") {
		t.Fatalf("GenerateLock() unsupported app error = %v", err)
	}

	release, err := storelock.Clear(context.Background(), env.GetStorePath())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, err := withLockWorkspace(ctx, func(context.Context, string) (string, error) {
			return "", errors.New("callback should not run")
		})
		result <- err
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("withLockWorkspace() canceled error = %v, want context.Canceled", err)
	}
	release()
	if entries, readErr := os.ReadDir(filepath.Join(env.GetStorePath(), ".lockfiles")); readErr == nil && len(entries) != 0 {
		t.Fatalf("canceled acquisition created workspace entries: %v", entries)
	} else if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
}

func TestGenerateLockRoutesBunUVAndGoPrerequisites(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	rm := New(config.MapOfRuntimes{})
	tests := []struct {
		name string
		app  binmanager.App
	}{
		{name: "bun", app: binmanager.App{Bun: &binmanager.AppConfigBun{PackageName: "example/tool", Version: "1.0.0", Runtime: "missing"}}},
		{name: "uv", app: binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "example/tool", Version: "1.0.0", Runtime: "missing"}}},
		{name: "go", app: binmanager.App{Go: &binmanager.AppConfigGo{PackageName: "example.invalid/tool", Version: "v1.0.0", Runtime: "missing"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := rm.GenerateLock(context.Background(), tt.name, tt.app); err == nil {
				t.Fatal("GenerateLock succeeded without the required runtime")
			}
		})
	}
}

func TestGenerateGoLockFromLocalFixtureUsesAndRestoresPrivateWorkspace(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("GOPROXY", "off")
	if err := runtimeconfig.Init(); err != nil {
		t.Fatal(err)
	}
	runtimes := config.MapOfRuntimes{
		"go": {Kind: config.RuntimeKindGo, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: "go", SystemVersion: "test"}},
	}
	rm := New(runtimes)
	app := binmanager.App{
		Go:    &binmanager.AppConfigGo{PackageName: "example.invalid/local/tool", Version: "v1.0.0", Runtime: "go", LockFile: "pinned"},
		Files: map[string]string{"go.mod": "module example.invalid/local\n\ngo 1.20\n"},
	}
	pinned, err := rm.GetGoAppPath("local-tool", app.Go, app.Files, app.Archives, "go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pinned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "sentinel"), []byte("pinned"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := rm.GenerateLock(context.Background(), "local-tool", app)
	if err != nil {
		t.Fatalf("GenerateLock() local fixture: %v", err)
	}
	mod, _, err := parseGoLockFile(content)
	if err != nil {
		t.Fatalf("parseGoLockFile() generated content: %v", err)
	}
	if !strings.Contains(mod, "module example.invalid/local") {
		t.Fatalf("generated go.mod = %q, want local fixture module", mod)
	}
	if app.Go.LockFile != "pinned" {
		t.Fatalf("GenerateLock mutated caller app lock file: %q", app.Go.LockFile)
	}
	if got, err := os.ReadFile(filepath.Join(pinned, "sentinel")); err != nil || string(got) != "pinned" {
		t.Fatalf("pinned installation changed: content=%q err=%v", got, err)
	}
	if entries, err := os.ReadDir(filepath.Join(env.GetStorePath(), ".lockfiles")); err == nil && len(entries) != 0 {
		t.Fatalf("private Go generation workspace leaked: %v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestGenerateLockUsesPrivateWorkspaceAndPreservesPinnedInstall(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(config.MapOfRuntimes{})
	app := binmanager.App{Node: &binmanager.AppConfigNode{
		PackageName: "example/tool", Version: "1.2.3", Runtime: "missing", LockFile: "pinned-lock",
	}}
	pinned := filepath.Join(env.GetStorePath(), ".apps", "node", "tool", "pinned")
	if err := os.MkdirAll(pinned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "sentinel"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := rm.GenerateLock(context.Background(), "tool", app); err == nil {
		t.Fatal("GenerateLock unexpectedly succeeded without a configured runtime")
	}
	if app.Node.LockFile != "pinned-lock" {
		t.Fatalf("pinned lock changed: %q", app.Node.LockFile)
	}
	if got, err := os.ReadFile(filepath.Join(pinned, "sentinel")); err != nil || string(got) != "keep" {
		t.Fatalf("pinned installation changed: content=%q err=%v", got, err)
	}
	if entries, err := os.ReadDir(filepath.Join(env.GetStorePath(), ".lockfiles")); err == nil && len(entries) != 0 {
		t.Fatalf("private generation workspace leaked: %v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestWithLockWorkspaceReturnsContentBeforeCleanup(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	want := "generated lock content"
	got, err := withLockWorkspace(context.Background(), func(_ context.Context, workspace string) (string, error) {
		if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
			t.Fatalf("workspace unavailable during generation: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "lock"), []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
		return want, nil
	})
	if err != nil || got != want {
		t.Fatalf("withLockWorkspace() = %q, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(env.GetStorePath(), ".lockfiles"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("workspace not cleaned: %v, %v", entries, err)
	}
}

func TestWithLockWorkspaceUsesDistinctCleanWorkspaces(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	const workers = 2
	started := make(chan string, workers)
	release := make(chan struct{})
	errs := make(chan error, workers)

	for range workers {
		go func() {
			content, err := withLockWorkspace(context.Background(), func(_ context.Context, workspace string) (string, error) {
				if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module isolated\n"), 0o644); err != nil {
					return "", err
				}
				started <- workspace
				<-release
				data, err := os.ReadFile(filepath.Join(workspace, "go.mod"))
				return string(data), err
			})
			if err == nil && content != "module isolated\n" {
				err = errors.New("generation content was not read before cleanup")
			}
			errs <- err
		}()
	}
	first, second := <-started, <-started
	if first == second {
		t.Fatalf("concurrent generations shared workspace %q", first)
	}
	close(release)
	for range workers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("workspace %q remains after generation, stat err=%v", path, err)
		}
	}
}

func TestWithLockWorkspaceCleansFailureAndReadOnlyCache(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	sentinel := errors.New("generation failed")
	var workspace string
	_, err := withLockWorkspace(context.Background(), func(_ context.Context, path string) (string, error) {
		workspace = path
		cache := filepath.Join(path, "gomodcache", "module@v1")
		if err := os.MkdirAll(cache, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(cache, "LICENSE"), []byte("data"), 0o444); err != nil {
			return "", err
		}
		if err := os.Chmod(cache, 0o555); err != nil {
			return "", err
		}
		return "", sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("withLockWorkspace error=%v, want wrapped generation error", err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Errorf("workspace %q remains after failure, stat err=%v", workspace, err)
	}
}

func TestWithLockWorkspaceGeneratesAndReleasesStoreLease(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	var workspace string
	content, err := withLockWorkspace(context.Background(), func(ctx context.Context, path string) (string, error) {
		workspace = path
		if _, ok := ctx.Value(lockWorkspaceKey{}).(string); !ok {
			t.Fatal("workspace context missing destination")
		}
		return "lock", nil
	})
	if err != nil || content != "lock" {
		t.Fatalf("withLockWorkspace() = %q, %v", content, err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace remains: %v", err)
	}
}

func TestGenerateLockFailureDoesNotModifyPinnedApp(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	path := filepath.Join(env.GetStorePath(), ".apps", "node", "tool", "pinned")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(path, "package-lock.json")
	if err := os.WriteFile(sentinel, []byte("pinned"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := withLockWorkspace(context.Background(), func(_ context.Context, workspace string) (string, error) {
		if workspace == path {
			t.Fatal("lock generation used installed app path")
		}
		if err := os.WriteFile(filepath.Join(workspace, "package-lock.json"), []byte("generated"), 0o644); err != nil {
			return "", err
		}
		return "", errors.New("generation failed")
	})
	if err == nil {
		t.Fatal("expected generation failure")
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "pinned" {
		t.Fatalf("pinned lockfile changed: content=%q err=%v", got, err)
	}
}

func TestWithLockWorkspaceReportsParentCreationFailure(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	parent := filepath.Join(env.GetStorePath(), ".lockfiles")
	if err := os.MkdirAll(env.GetStorePath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := withLockWorkspace(context.Background(), func(context.Context, string) (string, error) {
		called = true
		return "unexpected", nil
	})
	if err == nil || !strings.Contains(err.Error(), "create lockfile workspace parent") {
		t.Fatalf("withLockWorkspace() error = %v, want parent creation failure", err)
	}
	if called {
		t.Fatal("generation callback ran without a private workspace")
	}
}
