package runtimemanager

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/goccy/go-yaml"
)

func createTestTgz(t *testing.T, files map[string]string) string {
	t.Helper()

	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.tgz")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = tmpFile.Close() }()

	gzw := gzip.NewWriter(tmpFile)
	tw := tar.NewWriter(gzw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("failed to write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write tar content: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	return tmpFile.Name()
}

func TestExtractFullTgz(t *testing.T) {
	t.Run("extracts files correctly", func(t *testing.T) {
		archivePath := createTestTgz(t, map[string]string{
			"package/bin/pnpm.cjs":  "#!/usr/bin/env node\nconsole.log('pnpm');",
			"package/package.json":  `{"name":"pnpm","version":"9.0.0"}`,
			"package/bin/pnpmx.cjs": "#!/usr/bin/env node\nconsole.log('pnpmx');",
		})

		destDir := t.TempDir()
		if err := extractFullTgz(archivePath, destDir); err != nil {
			t.Fatalf("extractFullTgz() error = %v", err)
		}

		pnpmPath := filepath.Join(destDir, "package", "bin", "pnpm.cjs")
		if _, err := os.Stat(pnpmPath); err != nil {
			t.Errorf("pnpm.cjs not found: %v", err)
		}

		pkgPath := filepath.Join(destDir, "package", "package.json")
		if _, err := os.Stat(pkgPath); err != nil {
			t.Errorf("package.json not found: %v", err)
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		archivePath := createTestTgz(t, map[string]string{
			"../evil/file.txt": "malicious content",
			"safe/file.txt":    "safe content",
		})

		destDir := t.TempDir()
		if err := extractFullTgz(archivePath, destDir); err != nil {
			t.Fatalf("extractFullTgz() error = %v", err)
		}

		evilPath := filepath.Join(destDir, "..", "evil", "file.txt")
		if _, err := os.Stat(evilPath); err == nil {
			t.Error("path traversal file should not have been extracted")
		}

		safePath := filepath.Join(destDir, "safe", "file.txt")
		if _, err := os.Stat(safePath); err != nil {
			t.Error("safe file should have been extracted")
		}
	})

	t.Run("nonexistent archive", func(t *testing.T) {
		err := extractFullTgz("/nonexistent/archive.tgz", t.TempDir())
		if err == nil {
			t.Error("expected error for nonexistent archive")
		}
	})
}

// pnpmRegistryServers spins up a mock npm registry over TLS: one server serves
// the tarball at /tarball/pnpm.tgz and the version metadata on every other path,
// returning a dist.tarball that points at its own https URL. It also swaps
// pnpmHTTPClient for a client trusting the server's cert for the test's
// duration, because the download path now requires an https tarball URL. It
// returns the metadata base URL and the real SHA-256 of tgzData (the value a
// correct pnpmHash must equal).
func pnpmRegistryServers(t *testing.T, tgzData []byte, integrity string) (registryURL, pinnedHash string) {
	t.Helper()

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/tarball/pnpm.tgz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(tgzData)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		meta := map[string]any{
			"dist": map[string]any{
				"tarball":   srv.URL + "/tarball/pnpm.tgz",
				"integrity": integrity,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meta)
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	useTrustingPNPMClient(t, srv)

	sha256Sum := sha256.Sum256(tgzData)
	return srv.URL, hex.EncodeToString(sha256Sum[:])
}

// useTrustingPNPMClient swaps the package-level pnpmHTTPClient for a client that
// trusts srv's self-signed cert for the duration of the test, restoring the
// original afterwards. Needed because the https-only tarball guard requires the
// mock registry to speak over a secure transport.
func useTrustingPNPMClient(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := pnpmHTTPClient
	pnpmHTTPClient = srv.Client()
	t.Cleanup(func() { pnpmHTTPClient = orig })
}

func TestDownloadPNPMFromRegistry(t *testing.T) {
	tgzData := func(t *testing.T) []byte {
		t.Helper()
		path := createTestTgz(t, map[string]string{
			"package/bin/pnpm.cjs": "#!/usr/bin/env node\nconsole.log('pnpm');",
			"package/package.json": `{"name":"pnpm","version":"9.15.0"}`,
		})
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read tgz: %v", err)
		}
		return data
	}

	t.Run("downloads and extracts tarball", func(t *testing.T) {
		data := tgzData(t)
		sha512Sum := sha512.Sum512(data)
		integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])
		registryURL, pinnedHash := pnpmRegistryServers(t, data, integrity)

		destDir := t.TempDir()
		rm := New(config.MapOfRuntimes{})
		if err := rm.downloadPNPMFromRegistryURL(registryURL, "9.15.0", destDir, pinnedHash); err != nil {
			t.Fatalf("downloadPNPMFromRegistryURL() error = %v", err)
		}

		pnpmPath := filepath.Join(destDir, "package", "bin", "pnpm.cjs")
		if _, err := os.Stat(pnpmPath); err != nil {
			t.Errorf("pnpm.cjs not found after download: %v", err)
		}
	})

	t.Run("skips if already downloaded", func(t *testing.T) {
		destDir := t.TempDir()
		pnpmDir := filepath.Join(destDir, "package", "bin")
		if err := os.MkdirAll(pnpmDir, 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(pnpmDir, "pnpm.cjs"), []byte("already here"), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		rm := New(config.MapOfRuntimes{})
		err := rm.downloadPNPMFromRegistry("9.15.0", destDir, "test-pnpm-sha256-hash")
		if err != nil {
			t.Errorf("expected nil error for already downloaded, got %v", err)
		}
	})

	t.Run("pinned SHA-256 mismatch returns error", func(t *testing.T) {
		data := tgzData(t)
		sha512Sum := sha512.Sum512(data)
		integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])
		registryURL, _ := pnpmRegistryServers(t, data, integrity)

		wrongPinned := "0000000000000000000000000000000000000000000000000000000000000000"
		destDir := t.TempDir()
		rm := New(config.MapOfRuntimes{})
		err := rm.downloadPNPMFromRegistryURL(registryURL, "9.15.0", destDir, wrongPinned)
		if err == nil {
			t.Fatal("expected error for pinned SHA-256 mismatch")
		}
		if !strings.Contains(err.Error(), "SHA-256 hash mismatch") {
			t.Errorf("error should mention SHA-256 hash mismatch, got: %v", err)
		}
	})

	t.Run("SHA-512 integrity mismatch returns error", func(t *testing.T) {
		data := tgzData(t)
		wrongIntegrity := "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
		registryURL, pinnedHash := pnpmRegistryServers(t, data, wrongIntegrity)

		destDir := t.TempDir()
		rm := New(config.MapOfRuntimes{})
		err := rm.downloadPNPMFromRegistryURL(registryURL, "9.15.0", destDir, pinnedHash)
		if err == nil {
			t.Fatal("expected error for SHA-512 integrity mismatch")
		}
		if !strings.Contains(err.Error(), "SHA-512 integrity mismatch") {
			t.Errorf("error should mention SHA-512 integrity mismatch, got: %v", err)
		}
	})

	t.Run("sha1-only metadata rejected", func(t *testing.T) {
		data := tgzData(t)
		sha256Sum := sha256.Sum256(data)
		pinnedHash := hex.EncodeToString(sha256Sum[:])

		mux := http.NewServeMux()
		var srv *httptest.Server
		mux.HandleFunc("/tarball/pnpm.tgz", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(data)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			meta := map[string]any{
				"dist": map[string]any{
					"tarball": srv.URL + "/tarball/pnpm.tgz",
					"shasum":  "0000000000000000000000000000000000000000",
				},
			}
			_ = json.NewEncoder(w).Encode(meta)
		})
		srv = httptest.NewTLSServer(mux)
		defer srv.Close()
		useTrustingPNPMClient(t, srv)

		destDir := t.TempDir()
		rm := New(config.MapOfRuntimes{})
		err := rm.downloadPNPMFromRegistryURL(srv.URL, "9.15.0", destDir, pinnedHash)
		if err == nil {
			t.Fatal("expected error when only SHA-1 shasum is available")
		}
		if !strings.Contains(err.Error(), "SHA-512 integrity required") {
			t.Errorf("error should mention SHA-512 requirement, got: %v", err)
		}
	})

	t.Run("registry error returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		destDir := t.TempDir()
		rm := New(config.MapOfRuntimes{})
		err := rm.downloadPNPMFromRegistryURL(server.URL, "0.0.0-nonexistent", destDir, "irrelevant-hash")
		if err == nil {
			t.Error("expected error for registry error")
		}
	})
}

func TestVerifyPNPMIntegrity(t *testing.T) {
	testData := []byte("test tarball content")
	sha512Sum := sha512.Sum512(testData)
	sha512B64 := base64.StdEncoding.EncodeToString(sha512Sum[:])

	t.Run("valid SHA-512 integrity", func(t *testing.T) {
		meta := npmVersionMeta{}
		meta.Dist.Integrity = "sha512-" + sha512B64
		meta.Dist.Shasum = "ignored-sha1"
		err := verifyPNPMIntegrity(meta, sha512Sum[:])
		if err != nil {
			t.Errorf("expected no error with valid SHA-512, got: %v", err)
		}
	})

	t.Run("rejects SHA-1 only", func(t *testing.T) {
		meta := npmVersionMeta{}
		meta.Dist.Shasum = "abc123"
		err := verifyPNPMIntegrity(meta, sha512Sum[:])
		if err == nil {
			t.Error("expected error when only SHA-1 shasum is available")
		}
		if !strings.Contains(err.Error(), "SHA-512 integrity required") {
			t.Errorf("error should mention SHA-512 requirement, got: %v", err)
		}
	})

	t.Run("SHA-512 mismatch returns error", func(t *testing.T) {
		meta := npmVersionMeta{}
		wrongHash := make([]byte, 64)
		meta.Dist.Integrity = "sha512-" + base64.StdEncoding.EncodeToString(wrongHash)
		err := verifyPNPMIntegrity(meta, sha512Sum[:])
		if err == nil {
			t.Error("expected error for SHA-512 mismatch")
		}
	})

	t.Run("no integrity or shasum returns error", func(t *testing.T) {
		meta := npmVersionMeta{}
		err := verifyPNPMIntegrity(meta, sha512Sum[:])
		if err == nil {
			t.Error("expected error when no integrity or shasum")
		}
	})
}

func TestVerifyPNPMPinnedHash(t *testing.T) {
	testData := []byte("test tarball content for sha256")
	sha256Sum := sha256.Sum256(testData)
	sha256Hex := hex.EncodeToString(sha256Sum[:])

	t.Run("valid SHA-256 pinned hash", func(t *testing.T) {
		err := verifyPNPMPinnedHash(sha256Hex, sha256Sum[:])
		if err != nil {
			t.Errorf("expected no error with valid SHA-256 pinned hash, got: %v", err)
		}
	})

	t.Run("empty pinned hash returns error", func(t *testing.T) {
		err := verifyPNPMPinnedHash("", sha256Sum[:])
		if err == nil {
			t.Error("expected error when pinned hash is empty")
		}
		if !strings.Contains(err.Error(), "pnpm tarball SHA-256 hash is required") {
			t.Errorf("error should mention hash is required, got: %v", err)
		}
	})

	t.Run("mismatched pinned hash returns error", func(t *testing.T) {
		wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
		err := verifyPNPMPinnedHash(wrongHash, sha256Sum[:])
		if err == nil {
			t.Error("expected error for hash mismatch")
		}
		if !strings.Contains(err.Error(), "SHA-256 hash mismatch") {
			t.Errorf("error should mention hash mismatch, got: %v", err)
		}
	})
}

func TestDownloadPNPMWithIntegrity(t *testing.T) {
	tgzPath := createTestTgz(t, map[string]string{
		"package/bin/pnpm.cjs": "#!/usr/bin/env node\nconsole.log('pnpm');",
		"package/package.json": `{"name":"pnpm","version":"9.15.0"}`,
	})

	tgzData, err := os.ReadFile(tgzPath)
	if err != nil {
		t.Fatalf("failed to read tgz: %v", err)
	}

	sha512Sum := sha512.Sum512(tgzData)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])
	registryURL, pinnedHash := pnpmRegistryServers(t, tgzData, integrity)

	destDir := t.TempDir()
	rm := New(config.MapOfRuntimes{})
	if err := rm.downloadPNPMFromRegistryURL(registryURL, "9.15.0", destDir, pinnedHash); err != nil {
		t.Fatalf("downloadPNPMFromRegistryURL() with integrity error = %v", err)
	}

	pnpmPath := filepath.Join(destDir, "package", "bin", "pnpm.cjs")
	if _, err := os.Stat(pnpmPath); err != nil {
		t.Errorf("pnpm.cjs not found after download: %v", err)
	}
}

// TestDownloadPNPMFromRegistryURL_RejectsHTTPTarball pins review #9: even with a
// valid pinned hash supplied, a registry response whose dist.tarball is a
// plaintext http:// URL must be refused (no transport downgrade).
func TestDownloadPNPMFromRegistryURL_RejectsHTTPTarball(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"dist":{"tarball":"http://insecure.example/pnpm.tgz","integrity":"sha512-AAAA"}}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	useTrustingPNPMClient(t, srv)

	destDir := t.TempDir()
	rm := New(config.MapOfRuntimes{})
	pinned := "0000000000000000000000000000000000000000000000000000000000000000"
	err := rm.downloadPNPMFromRegistryURL(srv.URL, "9.15.0", destDir, pinned)
	if err == nil {
		t.Fatal("expected error for http (non-https) tarball URL")
	}
	if !strings.Contains(err.Error(), "tarball URL is not https") {
		t.Errorf("error should mention non-https tarball, got: %v", err)
	}
}

// TestDownloadPNPMFromRegistryURL_HTTPSTarballSucceeds is the success-path
// counterpart: an https tarball still downloads, verifies, and extracts
// end-to-end via the mock registry.
func TestDownloadPNPMFromRegistryURL_HTTPSTarballSucceeds(t *testing.T) {
	path := createTestTgz(t, map[string]string{
		"package/bin/pnpm.cjs": "#!/usr/bin/env node\nconsole.log('pnpm');",
		"package/package.json": `{"name":"pnpm","version":"9.15.0"}`,
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read tgz: %v", err)
	}
	sha512Sum := sha512.Sum512(data)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])
	registryURL, pinnedHash := pnpmRegistryServers(t, data, integrity)

	destDir := t.TempDir()
	rm := New(config.MapOfRuntimes{})
	if err := rm.downloadPNPMFromRegistryURL(registryURL, "9.15.0", destDir, pinnedHash); err != nil {
		t.Fatalf("downloadPNPMFromRegistryURL() over https error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "package", "bin", "pnpm.cjs")); err != nil {
		t.Errorf("pnpm.cjs not found after https download: %v", err)
	}
}

func TestNpmVersionMeta(t *testing.T) {
	t.Run("deserialization", func(t *testing.T) {
		jsonData := `{"dist":{"tarball":"https://registry.npmjs.org/pnpm/-/pnpm-9.15.0.tgz","shasum":"abc123","integrity":"sha512-AAAA"}}`
		var meta npmVersionMeta
		if err := json.Unmarshal([]byte(jsonData), &meta); err != nil {
			t.Fatalf("Unmarshal error = %v", err)
		}
		if meta.Dist.Tarball != "https://registry.npmjs.org/pnpm/-/pnpm-9.15.0.tgz" {
			t.Errorf("Tarball = %q, want expected URL", meta.Dist.Tarball)
		}
		if meta.Dist.Shasum != "abc123" {
			t.Errorf("Shasum = %q, want %q", meta.Dist.Shasum, "abc123")
		}
		if meta.Dist.Integrity != "sha512-AAAA" {
			t.Errorf("Integrity = %q, want %q", meta.Dist.Integrity, "sha512-AAAA")
		}
	})

	t.Run("deserialization without integrity", func(t *testing.T) {
		jsonData := `{"dist":{"tarball":"https://registry.npmjs.org/pnpm/-/pnpm-9.15.0.tgz","shasum":"abc123"}}`
		var meta npmVersionMeta
		if err := json.Unmarshal([]byte(jsonData), &meta); err != nil {
			t.Fatalf("Unmarshal error = %v", err)
		}
		if meta.Dist.Integrity != "" {
			t.Errorf("Integrity = %q, want empty string", meta.Dist.Integrity)
		}
	})
}

func TestBuildPNPMInstallArgs(t *testing.T) {
	t.Run("without lockfile", func(t *testing.T) {
		args := buildPNPMInstallArgs("/path/to/pnpm.cjs", false)
		expected := []string{"/path/to/pnpm.cjs", "install"}
		if len(args) != len(expected) {
			t.Fatalf("args length = %d, want %d", len(args), len(expected))
		}
		for i, arg := range args {
			if arg != expected[i] {
				t.Errorf("args[%d] = %q, want %q", i, arg, expected[i])
			}
		}
	})

	t.Run("with lockfile includes --frozen-lockfile", func(t *testing.T) {
		args := buildPNPMInstallArgs("/path/to/pnpm.cjs", true)
		expected := []string{"/path/to/pnpm.cjs", "install", "--frozen-lockfile"}
		if len(args) != len(expected) {
			t.Fatalf("args length = %d, want %d", len(args), len(expected))
		}
		for i, arg := range args {
			if arg != expected[i] {
				t.Errorf("args[%d] = %q, want %q", i, arg, expected[i])
			}
		}
	})
}

// TestFilesWithMergedWorkspaceYAML_HashIncludesDefaults pins the invariant
// that the node app cache key incorporates the merged pnpm-workspace.yaml
// content (defaults + user override), not just the user override. Without
// this, tightening defaultPNPMWorkspaceConfig in a future release would not
// invalidate existing installs.
func TestFilesWithMergedWorkspaceYAML_HashIncludesDefaults(t *testing.T) {
	t.Run("nil files map gets injected workspace yaml", func(t *testing.T) {
		out, err := filesWithMergedWorkspaceYAML(nil)
		if err != nil {
			t.Fatalf("filesWithMergedWorkspaceYAML() error = %v", err)
		}
		got, ok := out["pnpm-workspace.yaml"]
		if !ok {
			t.Fatal("output missing pnpm-workspace.yaml entry")
		}
		if !strings.Contains(got, "strictDepBuilds") {
			t.Errorf("injected yaml missing security defaults; got: %q", got)
		}
	})

	t.Run("input files map is not mutated", func(t *testing.T) {
		files := map[string]string{
			".npmrc": "registry=https://registry.npmjs.org/\n",
		}
		_, err := filesWithMergedWorkspaceYAML(files)
		if err != nil {
			t.Fatalf("filesWithMergedWorkspaceYAML() error = %v", err)
		}
		if _, has := files["pnpm-workspace.yaml"]; has {
			t.Error("caller's files map was mutated with workspace entry")
		}
	})

	t.Run("user override is merged into injected entry", func(t *testing.T) {
		files := map[string]string{
			"pnpm-workspace.yaml": "allowBuilds:\n  puppeteer: true\n",
		}
		out, err := filesWithMergedWorkspaceYAML(files)
		if err != nil {
			t.Fatalf("filesWithMergedWorkspaceYAML() error = %v", err)
		}
		got := out["pnpm-workspace.yaml"]
		if !strings.Contains(got, "strictDepBuilds") {
			t.Errorf("merged yaml missing security defaults; got: %q", got)
		}
		if !strings.Contains(got, "puppeteer") {
			t.Errorf("merged yaml missing user override; got: %q", got)
		}
	})
}

func TestDefaultPNPMWorkspaceConfig(t *testing.T) {
	cfg := defaultPNPMWorkspaceConfig()

	expected := map[string]any{
		"strictDepBuilds":           true,
		"blockExoticSubdeps":        true,
		"enablePrePostScripts":      false,
		"dangerouslyAllowAllBuilds": false,
		"minimumReleaseAge":         10080,
		"trustPolicy":               "no-downgrade",
		"lockfile":                  true,
		"preferFrozenLockfile":      true,
	}

	if len(cfg) != len(expected) {
		t.Errorf("config has %d keys, want %d (got %v)", len(cfg), len(expected), cfg)
	}

	for key, want := range expected {
		got, ok := cfg[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if !pnpmWorkspaceValueEqual(got, want) {
			t.Errorf("key %q = %v (%T), want %v (%T)", key, got, got, want, want)
		}
	}
}

func pnpmWorkspaceValueEqual(got, want any) bool {
	if g, ok := got.(int); ok {
		if w, ok := want.(int); ok {
			return g == w
		}
	}
	if g, ok := got.(uint64); ok {
		if w, ok := want.(int); ok {
			return g == uint64(w)
		}
	}
	if g, ok := got.(int64); ok {
		if w, ok := want.(int); ok {
			return g == int64(w)
		}
	}
	return got == want
}

func TestMergePNPMWorkspaceConfig(t *testing.T) {
	t.Run("empty user YAML returns defaults unchanged", func(t *testing.T) {
		defaults := defaultPNPMWorkspaceConfig()
		merged, err := mergePNPMWorkspaceConfig(defaults, "")
		if err != nil {
			t.Fatalf("mergePNPMWorkspaceConfig() error = %v", err)
		}

		if len(merged) != len(defaults) {
			t.Errorf("merged has %d keys, want %d (defaults: %v, merged: %v)", len(merged), len(defaults), defaults, merged)
		}
		for key, want := range defaults {
			got, ok := merged[key]
			if !ok {
				t.Errorf("missing key %q in merged", key)
				continue
			}
			if !pnpmWorkspaceValueEqual(got, want) {
				t.Errorf("key %q = %v, want %v", key, got, want)
			}
		}
	})

	t.Run("user adds allowBuilds without touching defaults", func(t *testing.T) {
		defaults := defaultPNPMWorkspaceConfig()
		userYAML := "allowBuilds:\n  puppeteer: true\n"

		merged, err := mergePNPMWorkspaceConfig(defaults, userYAML)
		if err != nil {
			t.Fatalf("mergePNPMWorkspaceConfig() error = %v", err)
		}

		allowBuilds, ok := merged["allowBuilds"]
		if !ok {
			t.Fatal("merged result missing allowBuilds key")
		}
		switch ab := allowBuilds.(type) {
		case map[string]any:
			if v, ok := ab["puppeteer"]; !ok || v != true {
				t.Errorf("allowBuilds.puppeteer = %v, want true", v)
			}
		case map[any]any:
			if v, ok := ab["puppeteer"]; !ok || v != true {
				t.Errorf("allowBuilds.puppeteer = %v, want true", v)
			}
		default:
			t.Errorf("allowBuilds has unexpected type %T: %v", allowBuilds, allowBuilds)
		}

		if !pnpmWorkspaceValueEqual(merged["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true (default preserved)", merged["strictDepBuilds"])
		}
		if !pnpmWorkspaceValueEqual(merged["blockExoticSubdeps"], true) {
			t.Errorf("blockExoticSubdeps = %v, want true (default preserved)", merged["blockExoticSubdeps"])
		}
		if !pnpmWorkspaceValueEqual(merged["minimumReleaseAge"], 10080) {
			t.Errorf("minimumReleaseAge = %v, want 10080 (default preserved)", merged["minimumReleaseAge"])
		}
	})

	t.Run("user overrides strictDepBuilds (user wins)", func(t *testing.T) {
		defaults := defaultPNPMWorkspaceConfig()
		userYAML := "strictDepBuilds: false\n"

		merged, err := mergePNPMWorkspaceConfig(defaults, userYAML)
		if err != nil {
			t.Fatalf("mergePNPMWorkspaceConfig() error = %v", err)
		}

		if !pnpmWorkspaceValueEqual(merged["strictDepBuilds"], false) {
			t.Errorf("strictDepBuilds = %v, want false (user override should win)", merged["strictDepBuilds"])
		}

		if !pnpmWorkspaceValueEqual(merged["blockExoticSubdeps"], true) {
			t.Errorf("blockExoticSubdeps = %v, want true (default preserved)", merged["blockExoticSubdeps"])
		}
	})

	t.Run("invalid YAML returns error", func(t *testing.T) {
		defaults := defaultPNPMWorkspaceConfig()
		_, err := mergePNPMWorkspaceConfig(defaults, "not: valid: yaml: at: all: [")
		if err == nil {
			t.Error("expected error for invalid YAML, got nil")
		}
	})
}

func TestBuildPNPMWorkspaceForApp(t *testing.T) {
	t.Run("no user override returns defaults", func(t *testing.T) {
		yamlOut, err := buildPNPMWorkspaceForApp(nil)
		if err != nil {
			t.Fatalf("buildPNPMWorkspaceForApp() error = %v", err)
		}
		if yamlOut == "" {
			t.Fatal("expected non-empty YAML output")
		}

		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlOut), &parsed); err != nil {
			t.Fatalf("failed to parse output YAML: %v", err)
		}

		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true", parsed["strictDepBuilds"])
		}
		if !pnpmWorkspaceValueEqual(parsed["blockExoticSubdeps"], true) {
			t.Errorf("blockExoticSubdeps = %v, want true", parsed["blockExoticSubdeps"])
		}
		if !pnpmWorkspaceValueEqual(parsed["minimumReleaseAge"], 10080) {
			t.Errorf("minimumReleaseAge = %v, want 10080", parsed["minimumReleaseAge"])
		}
		if !pnpmWorkspaceValueEqual(parsed["trustPolicy"], "no-downgrade") {
			t.Errorf("trustPolicy = %v, want \"no-downgrade\"", parsed["trustPolicy"])
		}
	})

	t.Run("empty files map returns defaults", func(t *testing.T) {
		yamlOut, err := buildPNPMWorkspaceForApp(map[string]string{})
		if err != nil {
			t.Fatalf("buildPNPMWorkspaceForApp() error = %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlOut), &parsed); err != nil {
			t.Fatalf("failed to parse output YAML: %v", err)
		}
		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true", parsed["strictDepBuilds"])
		}
	})

	t.Run("files without pnpm-workspace.yaml entry returns defaults", func(t *testing.T) {
		files := map[string]string{
			".npmrc": "registry=https://registry.npmjs.org/\n",
		}
		yamlOut, err := buildPNPMWorkspaceForApp(files)
		if err != nil {
			t.Fatalf("buildPNPMWorkspaceForApp() error = %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlOut), &parsed); err != nil {
			t.Fatalf("failed to parse output YAML: %v", err)
		}
		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true", parsed["strictDepBuilds"])
		}
		if _, hasAllowBuilds := parsed["allowBuilds"]; hasAllowBuilds {
			t.Error("output should not have allowBuilds when no user override provided")
		}
	})

	t.Run("user pnpm-workspace.yaml entry is merged with defaults", func(t *testing.T) {
		files := map[string]string{
			"pnpm-workspace.yaml": "allowBuilds:\n  puppeteer: true\n",
		}
		yamlOut, err := buildPNPMWorkspaceForApp(files)
		if err != nil {
			t.Fatalf("buildPNPMWorkspaceForApp() error = %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlOut), &parsed); err != nil {
			t.Fatalf("failed to parse output YAML: %v", err)
		}

		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true (default preserved)", parsed["strictDepBuilds"])
		}
		if !pnpmWorkspaceValueEqual(parsed["blockExoticSubdeps"], true) {
			t.Errorf("blockExoticSubdeps = %v, want true (default preserved)", parsed["blockExoticSubdeps"])
		}

		allowBuilds, ok := parsed["allowBuilds"]
		if !ok {
			t.Fatal("merged result missing allowBuilds key")
		}
		switch ab := allowBuilds.(type) {
		case map[string]any:
			if v, ok := ab["puppeteer"]; !ok || v != true {
				t.Errorf("allowBuilds.puppeteer = %v, want true", v)
			}
		case map[any]any:
			if v, ok := ab["puppeteer"]; !ok || v != true {
				t.Errorf("allowBuilds.puppeteer = %v, want true", v)
			}
		default:
			t.Errorf("allowBuilds has unexpected type %T: %v", allowBuilds, allowBuilds)
		}
	})

	t.Run("user override of security setting wins", func(t *testing.T) {
		files := map[string]string{
			"pnpm-workspace.yaml": "strictDepBuilds: false\n",
		}
		yamlOut, err := buildPNPMWorkspaceForApp(files)
		if err != nil {
			t.Fatalf("buildPNPMWorkspaceForApp() error = %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlOut), &parsed); err != nil {
			t.Fatalf("failed to parse output YAML: %v", err)
		}

		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], false) {
			t.Errorf("strictDepBuilds = %v, want false (user override should win)", parsed["strictDepBuilds"])
		}
		if !pnpmWorkspaceValueEqual(parsed["blockExoticSubdeps"], true) {
			t.Errorf("blockExoticSubdeps = %v, want true (default preserved)", parsed["blockExoticSubdeps"])
		}
	})
}

func prepareAndWriteWorkspace(t *testing.T, appEnvPath string, files map[string]string) map[string]string {
	t.Helper()
	mergedYAML, filtered, err := preparePNPMWorkspaceForApp(files)
	if err != nil {
		t.Fatalf("preparePNPMWorkspaceForApp() error = %v", err)
	}
	if err := writeAppWorkspaceFile(appEnvPath, mergedYAML); err != nil {
		t.Fatalf("writeAppWorkspaceFile() error = %v", err)
	}
	return filtered
}

func TestWriteAppWorkspaceFile(t *testing.T) {
	t.Run("nil files writes defaults to disk", func(t *testing.T) {
		appEnvPath := t.TempDir()

		filtered := prepareAndWriteWorkspace(t, appEnvPath, nil)
		if filtered != nil {
			t.Errorf("filtered files = %v, want nil for nil input", filtered)
		}

		workspacePath := filepath.Join(appEnvPath, "pnpm-workspace.yaml")
		content, err := os.ReadFile(workspacePath)
		if err != nil {
			t.Fatalf("failed to read written file: %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to parse written YAML: %v", err)
		}
		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true (default)", parsed["strictDepBuilds"])
		}
		if !pnpmWorkspaceValueEqual(parsed["minimumReleaseAge"], 10080) {
			t.Errorf("minimumReleaseAge = %v, want 10080 (default)", parsed["minimumReleaseAge"])
		}
	})

	t.Run("files without workspace entry returns same map untouched", func(t *testing.T) {
		appEnvPath := t.TempDir()
		files := map[string]string{
			".npmrc": "registry=https://registry.npmjs.org/\n",
		}

		filtered := prepareAndWriteWorkspace(t, appEnvPath, files)
		if len(filtered) != 1 || filtered[".npmrc"] != files[".npmrc"] {
			t.Errorf("filtered = %v, want same map with .npmrc preserved", filtered)
		}

		workspacePath := filepath.Join(appEnvPath, "pnpm-workspace.yaml")
		content, err := os.ReadFile(workspacePath)
		if err != nil {
			t.Fatalf("failed to read written file: %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to parse written YAML: %v", err)
		}
		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true (default)", parsed["strictDepBuilds"])
		}
		if _, has := parsed["allowBuilds"]; has {
			t.Error("output should not contain allowBuilds when no user override provided")
		}
	})

	t.Run("user workspace entry produces merged file and is filtered out", func(t *testing.T) {
		appEnvPath := t.TempDir()
		files := map[string]string{
			"pnpm-workspace.yaml": "allowBuilds:\n  puppeteer: true\nstrictDepBuilds: false\n",
			".npmrc":              "registry=https://registry.npmjs.org/\n",
		}

		filtered := prepareAndWriteWorkspace(t, appEnvPath, files)

		if _, has := filtered["pnpm-workspace.yaml"]; has {
			t.Error("filtered files should not contain pnpm-workspace.yaml (consumed by merge)")
		}
		if filtered[".npmrc"] != files[".npmrc"] {
			t.Errorf("filtered[.npmrc] = %q, want unchanged", filtered[".npmrc"])
		}

		if _, has := files["pnpm-workspace.yaml"]; !has {
			t.Error("caller's files map should not have been mutated")
		}

		workspacePath := filepath.Join(appEnvPath, "pnpm-workspace.yaml")
		content, err := os.ReadFile(workspacePath)
		if err != nil {
			t.Fatalf("failed to read written file: %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to parse written YAML: %v", err)
		}

		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], false) {
			t.Errorf("strictDepBuilds = %v, want false (user override)", parsed["strictDepBuilds"])
		}
		if !pnpmWorkspaceValueEqual(parsed["blockExoticSubdeps"], true) {
			t.Errorf("blockExoticSubdeps = %v, want true (default preserved)", parsed["blockExoticSubdeps"])
		}
		allowBuilds, ok := parsed["allowBuilds"]
		if !ok {
			t.Fatal("written YAML missing allowBuilds")
		}
		switch ab := allowBuilds.(type) {
		case map[string]any:
			if v, ok := ab["puppeteer"]; !ok || v != true {
				t.Errorf("allowBuilds.puppeteer = %v, want true", v)
			}
		case map[any]any:
			if v, ok := ab["puppeteer"]; !ok || v != true {
				t.Errorf("allowBuilds.puppeteer = %v, want true", v)
			}
		default:
			t.Errorf("allowBuilds has unexpected type %T", allowBuilds)
		}
	})

	t.Run("creates app directory if missing", func(t *testing.T) {
		baseDir := t.TempDir()
		appEnvPath := filepath.Join(baseDir, "nested", "app-env")

		prepareAndWriteWorkspace(t, appEnvPath, nil)

		if _, err := os.Stat(filepath.Join(appEnvPath, "pnpm-workspace.yaml")); err != nil {
			t.Errorf("workspace file not created: %v", err)
		}
	})

	t.Run("invalid user YAML returns error", func(t *testing.T) {
		files := map[string]string{
			"pnpm-workspace.yaml": "not: valid: yaml: at: all: [",
		}

		_, _, err := preparePNPMWorkspaceForApp(files)
		if err == nil {
			t.Error("expected error for invalid user YAML, got nil")
		}
	})

	t.Run("post-write overwrites pre-existing file (archive ordering invariant)", func(t *testing.T) {
		appEnvPath := t.TempDir()

		archiveContent := "strictDepBuilds: false\nallowBuilds:\n  malicious: true\n"
		if err := os.WriteFile(filepath.Join(appEnvPath, "pnpm-workspace.yaml"), []byte(archiveContent), 0644); err != nil {
			t.Fatalf("failed to seed pre-existing workspace file: %v", err)
		}

		prepareAndWriteWorkspace(t, appEnvPath, nil)

		content, err := os.ReadFile(filepath.Join(appEnvPath, "pnpm-workspace.yaml"))
		if err != nil {
			t.Fatalf("failed to read workspace file: %v", err)
		}
		var parsed map[string]any
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to parse: %v", err)
		}
		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true (secure default must overwrite pre-existing file)", parsed["strictDepBuilds"])
		}
		if _, has := parsed["allowBuilds"]; has {
			t.Error("allowBuilds from pre-existing file leaked through; archive content must not survive the post-write")
		}
	})
}
