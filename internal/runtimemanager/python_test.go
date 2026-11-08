package runtimemanager

import (
	"context"
	"encoding/json"
	"errors"
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

func TestSelectPythonDownloadRejectsInvalidMatchingEntry(t *testing.T) {
	rm := New(nil)
	base := testUVRuntimeConfig(t, "uv", "3.14.6")
	uv := *base.UV
	entry := uv.PythonDownloads["test-cpython-host"]
	tests := []struct {
		name string
		edit func(*config.PythonDownload)
	}{
		{name: "wrong name", edit: func(d *config.PythonDownload) { d.Name = "pypy" }},
		{name: "prerelease", edit: func(d *config.PythonDownload) { d.Prerelease = "rc1" }},
		{name: "architecture variant", edit: func(d *config.PythonDownload) { d.Arch.Variant = "v1" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := entry
			tt.edit(&candidate)
			uv.PythonDownloads = map[string]config.PythonDownload{"candidate": candidate}
			if _, err := selectPythonDownload(rm, &uv); err == nil {
				t.Fatal("selectPythonDownload accepted an invalid matching catalog entry")
			}
		})
	}
}

func TestEnsurePythonRejectsNonUVBeforeProvisioning(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	rm := New(config.MapOfRuntimes{"node": {Kind: config.RuntimeKindNode}})
	if _, err := rm.EnsurePython(context.Background(), "node"); err == nil || !strings.Contains(err.Error(), "not a configured uv runtime") {
		t.Fatalf("EnsurePython error = %v, want kind rejection", err)
	}
}

func TestPythonCommandErrorIncludesOperationAndOutput(t *testing.T) {
	err := uvPythonCommandError("python find", []byte("no managed interpreter"), errors.New("exit status 2"))
	if err == nil || !strings.Contains(err.Error(), "python find") || !strings.Contains(err.Error(), "no managed interpreter") {
		t.Fatalf("uvPythonCommandError = %v, want operation and captured output", err)
	}
}

func TestWritePrivateAtomicRejectsInvalidDestinationAndCleansTemporary(t *testing.T) {
	dir := t.TempDir()
	if err := writePrivateAtomic(filepath.Join(dir, "missing", "marker"), []byte("data")); err == nil {
		t.Fatal("writePrivateAtomic succeeded when its parent directory was missing")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary files left behind after failed write: entries=%v err=%v", entries, err)
	}
	path := filepath.Join(dir, "marker")
	if err := writePrivateAtomic(path, []byte("ready")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("published marker mode = %v, error = %v", info, err)
	}
}

func TestResolveExecutableWithinRejectsEscapingAndMissingExecutables(t *testing.T) {
	root := t.TempDir()
	payload := filepath.Join(root, "python")
	if err := os.MkdirAll(payload, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveExecutableWithin(payload, outside); err == nil {
		t.Fatal("accepted executable outside Python payload")
	}
	if _, err := resolveExecutableWithin(payload, filepath.Join(payload, "missing")); err == nil {
		t.Fatal("accepted missing executable")
	}
	plain := filepath.Join(payload, "plain")
	if err := os.WriteFile(plain, []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveExecutableWithin(payload, plain); err == nil {
		t.Fatal("accepted non-executable file")
	}
}

func TestPythonProvisionEnvDoesNotLeakInheritedOverrides(t *testing.T) {
	got := pythonProvisionEnv(map[string]string{"UV_PYTHON_INSTALL_DIR": "/owned/python", "UV_CACHE_DIR": "/owned/cache"})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "UV_CONFIG_FILE=") || strings.Contains(joined, "UV_PYTHON_DOWNLOADS_JSON_URL=") || strings.Contains(joined, "VIRTUAL_ENV=") {
		t.Fatalf("unsafe inherited Python selector present: %v", got)
	}
	if !strings.Contains(joined, "UV_PYTHON_INSTALL_DIR=/owned/python") || !strings.Contains(joined, "UV_CACHE_DIR=/owned/cache") {
		t.Fatalf("owned Python environment missing overrides: %v", got)
	}
}

func TestPythonCommandErrorExplainsUnsupportedSafetyFlags(t *testing.T) {
	err := uvPythonCommandError("python install", []byte("error: unexpected argument '--managed-python'"), errors.New("exit status 2"))
	if err == nil || !strings.Contains(err.Error(), "upgrade uv") || !strings.Contains(err.Error(), "python install") {
		t.Fatalf("unsupported safe-flag error = %v", err)
	}
}

func TestPythonCatalogTargetMapsSupportedAndRejectsUnknownPlatforms(t *testing.T) {
	tests := []struct {
		name, os, arch, libc, wantOS, wantArch, wantLibc string
		wantErr                                          bool
	}{
		{name: "glibc", os: "linux", arch: "amd64", libc: "glibc", wantOS: "linux", wantArch: "x86_64", wantLibc: "gnu"},
		{name: "musl", os: "linux", arch: "arm64", libc: "musl", wantOS: "linux", wantArch: "aarch64", wantLibc: "musl"},
		{name: "darwin", os: "darwin", arch: "arm64", libc: "glibc", wantOS: "darwin", wantArch: "aarch64", wantLibc: "none"},
		{name: "unknown libc", os: "linux", arch: "amd64", libc: "other", wantErr: true},
		{name: "unknown architecture", os: "linux", arch: "ppc64", libc: "glibc", wantErr: true},
		{name: "unknown OS", os: "freebsd", arch: "amd64", libc: "none", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osName, archName, libcName, err := pythonCatalogTarget(tt.os, tt.arch, tt.libc)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("pythonCatalogTarget unexpectedly accepted %s/%s/%s", tt.os, tt.arch, tt.libc)
				}
				return
			}
			if err != nil || osName != tt.wantOS || archName != tt.wantArch || libcName != tt.wantLibc {
				t.Fatalf("pythonCatalogTarget = (%q, %q, %q, %v)", osName, archName, libcName, err)
			}
		})
	}
}

func TestExactPythonVersionRejectsAmbiguousNumbers(t *testing.T) {
	for _, version := range []string{"", "3", "3.14", "03.14.6", "3.014.6", "3.14.06", "3.14.6rc1", "3.-1.6", "3.14.6.0"} {
		if isExactPythonVersion(version) {
			t.Errorf("isExactPythonVersion(%q) = true", version)
		}
	}
	for _, version := range []string{"0.0.0", "3.14.6", "10.2.0"} {
		if !isExactPythonVersion(version) {
			t.Errorf("isExactPythonVersion(%q) = false", version)
		}
	}
}

func TestSelectPythonDownloadRejectsInvalidVersion(t *testing.T) {
	rm := New(nil)
	valid := testUVRuntimeConfig(t, "uv", "3.14.6").UV
	for _, version := range []string{"3.14", "03.14.6", "3.14.6rc1"} {
		candidate := *valid
		candidate.PythonVersion = version
		if _, err := selectPythonDownload(rm, &candidate); err == nil || !strings.Contains(err.Error(), "exact major.minor.patch") {
			t.Errorf("selectPythonDownload(%q) = %v", version, err)
		}
	}
}

func TestEnsurePythonUsesLocalPinnedCatalogAndPublishesReadiness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake uv fixture uses a POSIX executable")
	}
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	entry := testUVRuntimeConfig(t, "", "3.14.6")
	uvPath := filepath.Join(base, "uv")
	script := `#!/bin/sh
set -eu
if [ "$1" = python ] && [ "$2" = install ]; then
  [ "$3" = --no-config ]
  [ "$4" = --managed-python ]
  [ "$5" = --no-bin ]
  [ "$6" = --install-dir ]
  root="$7"
  mkdir -p "$root/bin"
  printf '#!/bin/sh\nexit 0\n' > "$root/bin/python"
  chmod 755 "$root/bin/python"
  printf '%s\n' "$root/bin/python" > "$DATAMITSU_CACHE_DIR/python.path"
  exit 0
fi
if [ "$1" = python ] && [ "$2" = find ]; then
  [ "$3" = --no-config ]
  [ "$4" = --offline ]
  [ "$5" = --managed-python ]
  cat "$DATAMITSU_CACHE_DIR/python.path"
  exit 0
fi
echo unexpected uv arguments >&2
exit 42
`
	if err := os.WriteFile(uvPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := entry
	runtimeConfig.System = &config.RuntimeConfigSystem{Command: uvPath}
	runtimeConfig.UV.PythonDownloads = map[string]config.PythonDownload{"host": runtimeConfig.UV.PythonDownloads["test-cpython-host"]}
	rm := New(config.MapOfRuntimes{"python": runtimeConfig})
	got, err := rm.EnsurePython(context.Background(), "python")
	if err != nil {
		t.Fatalf("EnsurePython() = %q, %v", got, err)
	}
	if resolved, err := rm.ResolvePython("python"); err != nil || resolved != got {
		t.Fatalf("ResolvePython after install = %q, %v", resolved, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(got)), pythonReadyFilename)); err != nil {
		t.Fatalf("Python readiness record not published: %v", err)
	}
	identityRoot := filepath.Dir(filepath.Dir(filepath.Dir(got)))
	if _, err := os.Stat(filepath.Join(identityRoot, pythonCatalogFilename)); !os.IsNotExist(err) {
		t.Fatalf("temporary local catalog remained after provisioning: %v", err)
	}
}

func TestValidatePythonDownloadRejectsNegativeVersionAndMalformedURL(t *testing.T) {
	entry := testUVRuntimeConfig(t, "uv", "3.14.6").UV.PythonDownloads["test-cpython-host"]
	tests := []struct {
		name string
		edit func(*config.PythonDownload)
	}{
		{name: "negative patch", edit: func(d *config.PythonDownload) { d.Patch = -1 }},
		{name: "empty host", edit: func(d *config.PythonDownload) { d.URL = "https:///python.tar.zst" }},
		{name: "invalid URL", edit: func(d *config.PythonDownload) { d.URL = "://broken" }},
		{name: "wrong implementation", edit: func(d *config.PythonDownload) { d.Name = "pypy" }},
		{name: "non-default GIL", edit: func(d *config.PythonDownload) { d.Variant = "freethreaded" }},
		{name: "bad digest", edit: func(d *config.PythonDownload) { d.Hash = "sha256:xyz" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := entry
			tt.edit(&candidate)
			if err := validatePythonDownload(candidate); err == nil {
				t.Fatal("validatePythonDownload accepted malformed entry")
			}
		})
	}
}

func TestWritePrivateAtomicRenameFailurePreservesExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "target")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	err := writePrivateAtomic(destination, []byte("replacement"))
	if err == nil || !strings.Contains(err.Error(), "publish file") {
		t.Fatalf("writePrivateAtomic(directory destination) = %v, want publish error", err)
	}
	info, statErr := os.Stat(destination)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("failed publish changed destination: info=%v err=%v", info, statErr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "target" {
		t.Fatalf("temporary file leaked after rename failure: %v", entries)
	}
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

func TestPythonReadyRecordRejectsMalformedAndNonExecutableMarkers(t *testing.T) {
	store := t.TempDir()
	identity := filepath.Join(store, ".runtime-data", "uv", "identity")
	python := filepath.Join(identity, "python")
	executable := filepath.Join(python, "bin", "python")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("python"), 0o644); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(python, pythonReadyFilename)
	for _, raw := range []string{"{", `{"schema":2,"executable":".runtime-data/uv/identity/python/bin/python"}`, `{"schema":1,"executable":"/tmp/python"}`, `{"schema":1,"executable":".runtime-data/uv/identity/other/python"}`} {
		if err := os.WriteFile(markerPath, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolvePythonReady(store, identity); err == nil {
			t.Errorf("resolvePythonReady accepted marker %s", raw)
		}
	}
	marker, err := json.Marshal(pythonReadyRecord{Schema: 1, Executable: filepath.ToSlash(strings.TrimPrefix(executable, store+string(filepath.Separator)))})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePythonReady(store, identity); err == nil {
		t.Fatal("accepted non-executable Python file")
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

func TestEnsurePythonRepairsUnmarkedPayload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake uv fixture uses a POSIX executable")
	}
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	rc := testUVRuntimeConfig(t, "", "3.14.6")
	uvPath := filepath.Join(base, "uv")
	if err := os.WriteFile(uvPath, []byte("#!/bin/sh\nset -eu\nif [ \"$2\" = install ]; then\n root=\"$7\"; mkdir -p \"$root/bin\"; printf '#!/bin/sh\\nexit 0\\n' > \"$root/bin/python\"; chmod 755 \"$root/bin/python\"; echo \"$root/bin/python\" > \"$DATAMITSU_CACHE_DIR/python.path\"; exit 0; fi\nif [ \"$2\" = find ]; then cat \"$DATAMITSU_CACHE_DIR/python.path\"; exit 0; fi\nexit 41\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rc.System = &config.RuntimeConfigSystem{Command: uvPath}
	rm := New(config.MapOfRuntimes{"python": rc})
	identity, err := rm.runtimeDataIdentity("python", rc)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(env.GetRuntimeDataPath("python", identity), "python")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "python"), []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := rm.EnsurePython(context.Background(), "python"); err != nil {
		t.Fatalf("EnsurePython repair: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, pythonReadyFilename)); err != nil {
		t.Fatalf("readiness marker missing after repair: %v", err)
	}
}

func TestEnsurePythonOfflinePreservesUnmarkedPythonPayload(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	t.Setenv("DATAMITSU_OFFLINE", "1")
	rc := testUVRuntimeConfig(t, "/must/not/run", "3.14.6")
	rm := New(config.MapOfRuntimes{"python": rc})
	identity, err := rm.runtimeDataIdentity("python", rc)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(env.GetRuntimeDataPath("python", identity), "python", "bin", "python")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("recoverable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := rm.EnsurePython(context.Background(), "python"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("EnsurePython error = %v", err)
	}
	if got, err := os.ReadFile(executable); err != nil || string(got) != "recoverable" {
		t.Fatalf("offline attempt changed payload: %q, %v", got, err)
	}
}

func TestResolvePythonReportsMissingRuntimeAndInvalidReadyPayload(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", base)
	rm := New(config.MapOfRuntimes{"python": testUVRuntimeConfig(t, "uv", "3.14.6")})
	if _, err := rm.ResolvePython("absent"); err == nil || !strings.Contains(err.Error(), "not a configured uv runtime") {
		t.Fatalf("ResolvePython unknown runtime = %v", err)
	}
	if _, err := rm.ResolvePython("python"); err == nil {
		t.Fatal("ResolvePython accepted missing ready payload")
	}
	python := seedTestPythonReady(t, rm, "python", "", "")
	if got, err := rm.ResolvePython("python"); err != nil || got != python {
		t.Fatalf("ResolvePython ready payload = %q, %v", got, err)
	}
	if err := os.Remove(python); err != nil {
		t.Fatal(err)
	}
	if _, err := rm.ResolvePython("python"); err == nil {
		t.Fatal("ResolvePython accepted stale ready record")
	}
}

func TestResolveExecutableWithinAcceptsExecutableSymlinkWithinPayload(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real-python")
	if err := os.WriteFile(target, []byte("python"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "python")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := resolveExecutableWithin(root, link)
	if err != nil || got != target {
		t.Fatalf("resolveExecutableWithin() = %q, %v; want %q", got, err, target)
	}
}

func TestResolvePythonReturnsValidatedReadyExecutable(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rm := New(config.MapOfRuntimes{"python": testUVRuntimeConfig(t, "unused-uv", "3.14.6")})
	want := seedTestPythonReady(t, rm, "python", "", "")
	got, err := rm.ResolvePython("python")
	if err != nil || got != want {
		t.Fatalf("ResolvePython() = %q, %v; want %q", got, err, want)
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
