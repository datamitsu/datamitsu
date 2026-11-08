package runtimemanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/target"
)

func TestSelectPythonDownloadRequiresOneExactHostEntry(t *testing.T) {
	rm := New(nil)
	host := target.HostTarget()
	libc := string(host.Libc)
	switch runtime.GOOS {
	case "linux":
		if host.Libc == target.LibcGlibc {
			libc = "gnu"
		}
	case "darwin", "windows":
		libc = "none"
	}
	arch := host.Arch
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	entry := config.PythonDownload{Name: "cpython", Arch: config.PythonDownloadArch{Family: arch}, OS: runtime.GOOS, LibC: libc, Major: 3, Minor: 14, Patch: 6, URL: "https://example.invalid/python.tar.zst", Hash: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	if runtime.GOOS == "linux" && host.Libc != target.LibcGlibc && host.Libc != target.LibcMusl {
		t.Skip("host libc cannot be mapped to an upstream catalog target")
	}
	t.Run("missing catalog", func(t *testing.T) {
		_, err := selectPythonDownload(rm, &config.RuntimeConfigUV{PythonVersion: "3.14.6"})
		if err == nil || !strings.Contains(err.Error(), "pythonDownloads with SHA-256 pins is required") {
			t.Fatalf("expected actionable missing-catalog error, got %v", err)
		}
	})
	t.Run("duplicate host entries", func(t *testing.T) {
		_, err := selectPythonDownload(rm, &config.RuntimeConfigUV{PythonVersion: "3.14.6", PythonDownloads: map[string]config.PythonDownload{"one": entry, "two": entry}})
		if err == nil || !strings.Contains(err.Error(), "found 2") {
			t.Fatalf("expected duplicate host match error, got %v", err)
		}
	})
	t.Run("version mismatch", func(t *testing.T) {
		_, err := selectPythonDownload(rm, &config.RuntimeConfigUV{PythonVersion: "3.14.7", PythonDownloads: map[string]config.PythonDownload{"one": entry}})
		if err == nil || !strings.Contains(err.Error(), "found 0") {
			t.Fatalf("expected exact version mismatch, got %v", err)
		}
	})
	t.Run("rejects non-default GIL variants", func(t *testing.T) {
		entry.Variant = "freethreaded"
		_, err := selectPythonDownload(rm, &config.RuntimeConfigUV{PythonVersion: "3.14.6", PythonDownloads: map[string]config.PythonDownload{"one": entry}})
		if err == nil {
			t.Fatal("expected non-default variant to be excluded")
		}
	})
}

func TestValidatePythonDownloadRequiresCanonicalSHA256AndHTTPURL(t *testing.T) {
	entry := config.PythonDownload{Name: "cpython", Arch: config.PythonDownloadArch{Family: "x86_64"}, OS: "linux", LibC: "gnu", Major: 3, Minor: 14, Patch: 6, URL: "https://example.invalid/cpython.tar.zst", Hash: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	if err := validatePythonDownload(entry); err != nil {
		t.Fatalf("valid pinned download rejected: %v", err)
	}
	entry.Hash = strings.TrimPrefix(entry.Hash, "sha256:")
	if err := validatePythonDownload(entry); err == nil {
		t.Fatal("expected bare hash to be rejected")
	}
	entry.Hash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	entry.URL = "http://example.invalid/cpython.tar.zst"
	if err := validatePythonDownload(entry); err != nil {
		t.Fatalf("valid HTTP download URL rejected: %v", err)
	}
	entry.URL = "file:///tmp/cpython.tar.zst"
	if err := validatePythonDownload(entry); err == nil {
		t.Fatal("expected unsupported file URL to be rejected")
	}
}

func TestPythonReadyRecordValidatesStoreAndPayloadPaths(t *testing.T) {
	store := t.TempDir()
	identity := filepath.Join(store, ".runtime-data", "uv", "identity")
	python := filepath.Join(identity, "python")
	executable := filepath.Join(python, "bin", "python")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(pythonReadyRecord{Schema: 1, Executable: filepath.ToSlash(strings.TrimPrefix(executable, store+string(filepath.Separator)))})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(python, pythonReadyFilename), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolvePythonReady(store, identity)
	if err != nil || got != executable {
		t.Fatalf("resolvePythonReady() = %q, %v; want %q", got, err, executable)
	}
	outside := filepath.Join(t.TempDir(), "python")
	if err := os.WriteFile(outside, []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, executable); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolvePythonReady(store, identity); err == nil {
		t.Fatal("expected symlink escaping the Python payload to be rejected")
	}
	marker, err = json.Marshal(pythonReadyRecord{Schema: 1, Executable: "../outside/python"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(python, pythonReadyFilename), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePythonReady(store, identity); err == nil {
		t.Fatal("expected lexical store escape to be rejected")
	}
}

func TestPythonProvisionEnvRemovesInheritedSelectionOverrides(t *testing.T) {
	for key, value := range map[string]string{
		"UV_PYTHON_DOWNLOADS_JSON_URL": "https://attacker.invalid/catalog.json",
		"UV_PYTHON_INSTALL_DIR":        "/attacker/python",
		"UV_CONFIG_FILE":               "/attacker/uv.toml",
		"VIRTUAL_ENV":                  "/attacker/venv",
		"CONDA_PREFIX":                 "/attacker/conda",
	} {
		t.Setenv(key, value)
	}
	env := pythonProvisionEnv(getUVEnvVars("/owned/runtime"))
	values := make(map[string]string, len(env))
	for _, pair := range env {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			values[key] = value
		}
	}
	if values["UV_PYTHON_INSTALL_DIR"] != filepath.Join("/owned/runtime", "python") {
		t.Fatalf("UV_PYTHON_INSTALL_DIR = %q", values["UV_PYTHON_INSTALL_DIR"])
	}
	for _, key := range []string{"UV_PYTHON_DOWNLOADS_JSON_URL", "UV_CONFIG_FILE", "VIRTUAL_ENV", "CONDA_PREFIX"} {
		if _, exists := values[key]; exists {
			t.Errorf("inherited %s was retained", key)
		}
	}
}

func TestMarshalPythonCatalogPreservesUpstreamKeyAndOmitsEmptyVariants(t *testing.T) {
	selected := selectedPythonDownload{Key: "cpython-3.14.6-x86_64-unknown-linux-gnu", Entry: config.PythonDownload{
		Name: "cpython", Arch: config.PythonDownloadArch{Family: "x86_64"}, OS: "linux", LibC: "gnu",
		Major: 3, Minor: 14, Patch: 6, URL: "https://example.invalid/cpython.tar.zst", Build: "build", Hash: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}}
	var got uvPythonCatalog
	catalog, err := marshalPythonCatalog(selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(catalog, &got); err != nil {
		t.Fatal(err)
	}
	entry, ok := got[selected.Key]
	if !ok || entry.SHA256 != strings.TrimPrefix(selected.Entry.Hash, "sha256:") {
		t.Fatalf("catalog did not preserve selected pin: %#v", got)
	}
	if entry.Variant != nil || entry.Arch.Variant != nil || entry.Prerelease != nil {
		t.Fatalf("empty optional fields must not be encoded as empty strings: %#v", entry)
	}
}

func TestEnsurePythonOfflineDoesNotAcquireOrMutate(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	t.Setenv("DATAMITSU_OFFLINE", "1")
	host := target.HostTarget()
	libc := string(host.Libc)
	switch runtime.GOOS {
	case "linux":
		if host.Libc == target.LibcGlibc {
			libc = "gnu"
		}
	case "darwin", "windows":
		libc = "none"
	}
	arch := host.Arch
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	entry := config.PythonDownload{
		Name: "cpython", Arch: config.PythonDownloadArch{Family: arch}, OS: runtime.GOOS, LibC: libc,
		Major: 3, Minor: 14, Patch: 6, URL: "https://example.invalid/python.tar.zst",
		Hash: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	rm := New(config.MapOfRuntimes{
		"isolated-uv": {
			Kind:   config.RuntimeKindUV,
			Mode:   config.RuntimeModeSystem,
			System: &config.RuntimeConfigSystem{Command: "must-not-be-looked-up"},
			UV:     &config.RuntimeConfigUV{PythonVersion: "3.14.6", PythonDownloads: map[string]config.PythonDownload{"host": entry}},
		},
	})
	if runtime.GOOS == "linux" && host.Libc != target.LibcGlibc && host.Libc != target.LibcMusl {
		t.Skip("host libc cannot be mapped to an upstream catalog target")
	}
	storeRoot := env.GetStorePath()
	dataRoot, _, err := rm.RuntimeOwnedEnvironment("isolated-uv", rm.mapOfRuntimes["isolated-uv"], "")
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dataRoot, "python", "bin", "python")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(storeRoot, executable)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(pythonReadyRecord{Schema: 1, Executable: filepath.ToSlash(relative)})
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dataRoot, "python", pythonReadyFilename)
	if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := rm.EnsurePython(context.Background(), "isolated-uv")
	if err != nil || got != executable {
		t.Fatalf("ready offline Python should bypass uv acquisition: got %q, %v", got, err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if _, err := rm.EnsurePython(context.Background(), "isolated-uv"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("expected offline provisioning refusal, got %v", err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("offline refusal removed recoverable Python payload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, pythonCatalogFilename)); !os.IsNotExist(err) {
		t.Fatalf("offline refusal wrote a local catalog: stat error %v", err)
	}
}

func testUVRuntimeConfig(t *testing.T, command, version string) config.RuntimeConfig {
	t.Helper()
	rm := New(nil)
	osType, archType, libc, err := rm.hostRuntimePlatform()
	if err != nil {
		t.Fatal(err)
	}
	osName, archName, catalogLibc, err := pythonCatalogTarget(string(osType), string(archType), libc)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		t.Fatalf("test Python version %q is not major.minor.patch", version)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("invalid test Python major version %q: %v", parts[0], err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("invalid test Python minor version %q: %v", parts[1], err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		t.Fatalf("invalid test Python patch version %q: %v", parts[2], err)
	}
	entry := config.PythonDownload{
		Name: "cpython", Arch: config.PythonDownloadArch{Family: archName}, OS: osName, LibC: catalogLibc,
		Major: major, Minor: minor, Patch: patch, URL: "https://example.invalid/test-cpython.tar.zst",
		Hash: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	return config.RuntimeConfig{
		Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem,
		System: &config.RuntimeConfigSystem{Command: command},
		UV:     &config.RuntimeConfigUV{PythonVersion: version, PythonDownloads: map[string]config.PythonDownload{"test-cpython-host": entry}},
	}
}

func seedTestPythonReady(t *testing.T, rm *RuntimeManager, runtimeName, sourcePython, sourceStdlib string) string {
	t.Helper()
	rc := rm.mapOfRuntimes[runtimeName]
	identity, err := rm.runtimeDataIdentity(runtimeName, rc)
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := env.GetStorePath()
	identityRoot := env.GetRuntimeDataPath(runtimeName, identity)
	pythonRoot := filepath.Join(identityRoot, "python")
	binDir := filepath.Join(pythonRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "python"
	if sourcePython != "" {
		resolved, err := filepath.EvalSymlinks(sourcePython)
		if err != nil {
			t.Fatal(err)
		}
		name = filepath.Base(resolved)
		if err := copyFile(resolved, filepath.Join(binDir, name)); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if sourceStdlib != "" {
		resolved, err := filepath.EvalSymlinks(sourceStdlib)
		if err != nil {
			t.Fatal(err)
		}
		stdlibDest := filepath.Join(pythonRoot, "lib", filepath.Base(resolved))
		if err := copyDir(resolved, stdlibDest); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(binDir, name)
	relative, err := filepath.Rel(storeRoot, executable)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(pythonReadyRecord{Schema: 1, Executable: filepath.ToSlash(relative)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pythonRoot, pythonReadyFilename), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	return executable
}
