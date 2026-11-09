package runtimemanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/storelock"
)

func TestEnsurePythonStaleMetadataDirectoryPreservesPayload(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	rc := testUVRuntimeConfig(t, "unused-uv", "3.14.6")
	rm := New(config.MapOfRuntimes{"uv": rc})
	executable := seedTestPythonReady(t, rm, "uv", "", "")
	ready := filepath.Join(filepath.Dir(filepath.Dir(executable)), pythonReadyFilename)
	if err := os.Remove(ready); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(ready, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ready, "keep"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rm.EnsurePython(context.Background(), "uv"); err == nil || !strings.Contains(err.Error(), "remove stale Python metadata") {
		t.Fatalf("EnsurePython() = %v, want stale metadata removal error", err)
	}
	if got, err := os.ReadFile(filepath.Join(ready, "keep")); err != nil || string(got) != "payload" {
		t.Fatalf("stale metadata payload = %q, %v", got, err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("existing interpreter was removed: %v", err)
	}
}

func TestEnsurePythonInvalidCacheRootCannotAcquireLease(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "cache")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATAMITSU_CACHE_DIR", file)
	rm := New(config.MapOfRuntimes{"uv": testUVRuntimeConfig(t, "missing-uv", "3.14.6")})
	if _, err := rm.EnsurePython(context.Background(), "uv"); err == nil {
		t.Fatal("EnsurePython accepted a file cache root")
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "keep" {
		t.Fatalf("cache root contents = %q, %v", got, err)
	}
}

func TestEnsurePythonMissingUVDoesNotMutatePython(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	rc := testUVRuntimeConfig(t, filepath.Join(base, "missing-uv"), "3.14.6")
	rm := New(config.MapOfRuntimes{"uv": rc})
	identity, err := rm.runtimeDataIdentity("uv", rc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rm.EnsurePython(context.Background(), "uv"); err == nil {
		t.Fatal("EnsurePython succeeded without uv")
	}
	if _, err := os.Stat(filepath.Join(env.GetRuntimeDataPath("uv", identity), "python")); !os.IsNotExist(err) {
		t.Fatalf("Python payload mutated before uv acquisition: %v", err)
	}
}

func TestEnsurePythonParentFileBlocksStoreRootCreation(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache")
	if err := os.WriteFile(cache, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	rm := New(config.MapOfRuntimes{"uv": testUVRuntimeConfig(t, "missing-uv", "3.14.6")})
	if _, err := rm.EnsurePython(context.Background(), "uv"); err == nil {
		t.Fatal("EnsurePython accepted a file blocking store creation")
	}
	if got, err := os.ReadFile(cache); err != nil || string(got) != "keep" {
		t.Fatalf("blocking file contents = %q, %v", got, err)
	}
}

func TestEnsurePythonReadyRecordPublicationFailureDoesNotMarkReady(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("fake uv helper requires a POSIX shell")
	}
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	fake := filepath.Join(base, "uv")
	body := "#!/bin/sh\n"
	body += "if [ \"$1 $2\" = \"python install\" ]; then while [ $# -gt 0 ]; do if [ \"$1\" = --install-dir ]; then shift; root=\"$1\"; mkdir -p \"$root/bin\"; printf '#!/bin/sh\\nexit 0\\n' > \"$root/bin/python\"; chmod +x \"$root/bin/python\"; break; fi; shift; done; exit 0; fi\n"
	body += "while [ $# -gt 0 ]; do if [ \"$1\" = --python-downloads-json-url ]; then shift; catalog=\"$1\"; fi; shift; done\n"
	body += "root=$(dirname \"$catalog\")/python; mkdir -p \"$root/.datamitsu-python-ready.json\"; printf '%s/bin/python' \"$root\"\n"
	if err := os.WriteFile(fake, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	rc := testUVRuntimeConfig(t, fake, "3.14.6")
	rm := New(config.MapOfRuntimes{"uv": rc})
	identity, err := rm.runtimeDataIdentity("uv", rc)
	if err != nil {
		t.Fatal(err)
	}
	pythonRoot := filepath.Join(env.GetRuntimeDataPath("uv", identity), "python")
	if _, err := rm.EnsurePython(context.Background(), "uv"); err == nil || !strings.Contains(err.Error(), "publish Python readiness record") {
		t.Fatalf("EnsurePython() = %v, want publication failure", err)
	}
	if info, err := os.Stat(filepath.Join(pythonRoot, pythonReadyFilename)); err != nil || !info.IsDir() {
		t.Fatalf("readiness destination changed: %v %v", info, err)
	}
	if _, err := rm.ResolvePython("uv"); err == nil {
		t.Fatal("failed publication exposed ready Python")
	}
}

func TestWritePrivateAtomicDirectoryDestinationPreservesContents(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "record")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "keep"), []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateAtomic(destination, []byte("replacement")); err == nil {
		t.Fatal("writePrivateAtomic replaced a directory")
	}
	if got, err := os.ReadFile(filepath.Join(destination, "keep")); err != nil || string(got) != "prior" {
		t.Fatalf("destination contents = %q, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "record" {
		t.Fatalf("temporary files after failure: %v, %v", entries, err)
	}
}

func TestEnsurePythonCanceledTargetWaiterReleasesLease(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	leaseCtx, releaseStore, err := storelock.WithStore(context.Background(), env.GetStorePath())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseStore()
	rc := testUVRuntimeConfig(t, os.Args[0], "3.14.6")
	rm := New(config.MapOfRuntimes{"uv": rc})
	identity, err := rm.runtimeDataIdentity("uv", rc)
	if err != nil {
		t.Fatal(err)
	}
	_, releaseTarget, err := lockAppTarget(leaseCtx, filepath.Join(env.GetRuntimeDataPath("uv", identity), "python"))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseTarget()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := rm.EnsurePython(ctx, "uv"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Python waiter did not cancel at the held target: %v", err)
	}
	releaseTarget()
	releaseStore()
	clearCtx, cancelClear := context.WithTimeout(context.Background(), time.Second)
	defer cancelClear()
	clearRelease, err := storelock.Clear(clearCtx, env.GetStorePath())
	if err != nil {
		t.Fatalf("clear blocked by canceled waiter lease: %v", err)
	}
	clearRelease()
}
