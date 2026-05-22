package runtimemanager

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/goccy/go-yaml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeFNMTestRuntimes() config.MapOfRuntimes {
	return config.MapOfRuntimes{
		"fnm": {
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeManaged,
			FNM: &config.RuntimeConfigFNM{
				NodeVersion: "20.11.1",
				PNPMVersion: "9.15.0",
				PNPMHash:    "test-pnpm-sha256-hash",
			},
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeDarwin: {
						syslist.ArchTypeAmd64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-darwin-amd64.tar.gz",
							Hash:        "fnm123",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
						syslist.ArchTypeArm64: {"unknown": binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-darwin-arm64.tar.gz",
							Hash:        "fnm123arm",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
					syslist.OsTypeLinux: {
						syslist.ArchTypeAmd64: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/fnm-linux-amd64.tar.gz",
							Hash:        "fnm456",
							ContentType: binmanager.BinContentTypeTarGz,
						}},
					},
				},
			},
		},
		"fnm-system": {
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeSystem,
			FNM: &config.RuntimeConfigFNM{
				NodeVersion: "20.11.1",
				PNPMVersion: "9.15.0",
				PNPMHash:    "test-pnpm-sha256-hash",
			},
			System: &config.RuntimeConfigSystem{
				Command: "/usr/local/bin/fnm",
			},
		},
	}
}

func TestGetFNMEnvVars(t *testing.T) {
	appEnvPath := "/cache/.apps/fnm/mmdc/abc123"
	vars := getFNMEnvVars(appEnvPath)

	if _, ok := vars["npm_config_store_dir"]; !ok {
		t.Error("missing npm_config_store_dir")
	}

	expectedVirtualStore := filepath.Join(appEnvPath, "node_modules", ".pnpm")
	if vars["npm_config_virtual_store_dir"] != expectedVirtualStore {
		t.Errorf("npm_config_virtual_store_dir = %q, want %q", vars["npm_config_virtual_store_dir"], expectedVirtualStore)
	}

	expectedGlobalDir := filepath.Join(appEnvPath, "global")
	if vars["npm_config_global_dir"] != expectedGlobalDir {
		t.Errorf("npm_config_global_dir = %q, want %q", vars["npm_config_global_dir"], expectedGlobalDir)
	}

	if len(vars) != 3 {
		t.Errorf("vars has %d entries, want 3", len(vars))
	}
}

func TestGetFNMEnvVarsDifferentPaths(t *testing.T) {
	path1 := "/cache/.apps/fnm/app1/hash1"
	path2 := "/cache/.apps/fnm/app2/hash2"

	vars1 := getFNMEnvVars(path1)
	vars2 := getFNMEnvVars(path2)

	if vars1["npm_config_virtual_store_dir"] == vars2["npm_config_virtual_store_dir"] {
		t.Error("different app paths should produce different virtual store dirs")
	}
	if vars1["npm_config_global_dir"] == vars2["npm_config_global_dir"] {
		t.Error("different app paths should produce different global dirs")
	}
	if vars1["npm_config_store_dir"] != vars2["npm_config_store_dir"] {
		t.Error("store dir should be shared across apps")
	}
}

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
			"package/bin/pnpm.cjs":    "#!/usr/bin/env node\nconsole.log('pnpm');",
			"package/package.json":    `{"name":"pnpm","version":"9.0.0"}`,
			"package/bin/pnpmx.cjs":   "#!/usr/bin/env node\nconsole.log('pnpmx');",
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

func TestDownloadPNPMFromRegistry(t *testing.T) {
	t.Run("downloads and extracts tarball", func(t *testing.T) {
		tgzPath := createTestTgz(t, map[string]string{
			"package/bin/pnpm.cjs":  "#!/usr/bin/env node\nconsole.log('pnpm');",
			"package/package.json":  `{"name":"pnpm","version":"9.15.0"}`,
		})

		tgzData, err := os.ReadFile(tgzPath)
		if err != nil {
			t.Fatalf("failed to read tgz: %v", err)
		}

		sha512Sum := sha512.Sum512(tgzData)
		integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])

		tarballServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tgzData)
		}))
		defer tarballServer.Close()

		metaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			meta := map[string]any{
				"dist": map[string]any{
					"tarball":   tarballServer.URL + "/pnpm-9.15.0.tgz",
					"integrity": integrity,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(meta)
		}))
		defer metaServer.Close()

		destDir := t.TempDir()
		err = downloadPNPMFromRegistryWithURL(metaServer.URL, "9.15.0", destDir)
		if err != nil {
			t.Fatalf("downloadPNPMFromRegistry() error = %v", err)
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

		runtimes := makeFNMTestRuntimes()
		rm := New(runtimes)
		err := rm.downloadPNPMFromRegistry("9.15.0", destDir, "test-pnpm-sha256-hash")
		if err != nil {
			t.Errorf("expected nil error for already downloaded, got %v", err)
		}
	})

	t.Run("hash mismatch returns error", func(t *testing.T) {
		tgzPath := createTestTgz(t, map[string]string{
			"package/bin/pnpm.cjs": "content",
		})

		tgzData, err := os.ReadFile(tgzPath)
		if err != nil {
			t.Fatalf("failed to read tgz: %v", err)
		}

		wrongHash := make([]byte, 64)
		wrongIntegrity := "sha512-" + base64.StdEncoding.EncodeToString(wrongHash)

		tarballServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(tgzData)
		}))
		defer tarballServer.Close()

		metaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			meta := map[string]any{
				"dist": map[string]any{
					"tarball":   tarballServer.URL + "/pnpm.tgz",
					"integrity": wrongIntegrity,
				},
			}
			_ = json.NewEncoder(w).Encode(meta)
		}))
		defer metaServer.Close()

		destDir := t.TempDir()
		err = downloadPNPMFromRegistryWithURL(metaServer.URL, "9.15.0", destDir)
		if err == nil {
			t.Error("expected error for hash mismatch")
		}
	})

	t.Run("sha1-only metadata rejected", func(t *testing.T) {
		tgzPath := createTestTgz(t, map[string]string{
			"package/bin/pnpm.cjs": "content",
		})

		tgzData, err := os.ReadFile(tgzPath)
		if err != nil {
			t.Fatalf("failed to read tgz: %v", err)
		}

		tarballServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(tgzData)
		}))
		defer tarballServer.Close()

		metaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			meta := map[string]any{
				"dist": map[string]any{
					"tarball": tarballServer.URL + "/pnpm.tgz",
					"shasum":  "0000000000000000000000000000000000000000",
				},
			}
			_ = json.NewEncoder(w).Encode(meta)
		}))
		defer metaServer.Close()

		destDir := t.TempDir()
		err = downloadPNPMFromRegistryWithURL(metaServer.URL, "9.15.0", destDir)
		if err == nil {
			t.Error("expected error when only SHA-1 shasum is available")
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
		err := downloadPNPMFromRegistryWithURL(server.URL, "0.0.0-nonexistent", destDir)
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

	tarballServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(tgzData)
	}))
	defer tarballServer.Close()

	metaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := map[string]any{
			"dist": map[string]any{
				"tarball":   tarballServer.URL + "/pnpm-9.15.0.tgz",
				"shasum":    "irrelevant-sha1",
				"integrity": integrity,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meta)
	}))
	defer metaServer.Close()

	destDir := t.TempDir()
	err = downloadPNPMFromRegistryWithURL(metaServer.URL, "9.15.0", destDir)
	if err != nil {
		t.Fatalf("downloadPNPMFromRegistry() with integrity error = %v", err)
	}

	pnpmPath := filepath.Join(destDir, "package", "bin", "pnpm.cjs")
	if _, err := os.Stat(pnpmPath); err != nil {
		t.Errorf("pnpm.cjs not found after download: %v", err)
	}
}

func TestInstallNodeVersionAlreadyInstalled(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	cacheRoot := t.TempDir()
	nodeVersion := "20.11.1"

	nodeBinPath := env.GetNodeBinaryPath(cacheRoot, nodeVersion)
	if err := os.MkdirAll(filepath.Dir(nodeBinPath), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(nodeBinPath, []byte("#!/bin/sh\necho ok"), 0755); err != nil {
		t.Fatalf("failed to write fake node: %v", err)
	}

	err := rm.installNodeVersion("/usr/local/bin/fnm", nodeVersion, cacheRoot)
	if err != nil {
		t.Errorf("installNodeVersion() error = %v, expected nil for already installed", err)
	}
}

func TestGetFNMCommandInfo(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	t.Run("returns correct command info", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		info, err := rm.GetFNMCommandInfo("mmdc", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo() error = %v", err)
		}

		if info.Type != "fnm" {
			t.Errorf("Type = %q, want %q", info.Type, "fnm")
		}

		if info.Command == "" {
			t.Error("Command is empty")
		}

		if info.Args != nil {
			t.Errorf("Args should be nil, got %v", info.Args)
		}

		expectedKeys := []string{"npm_config_store_dir", "npm_config_virtual_store_dir", "npm_config_global_dir", "PATH"}
		for _, key := range expectedKeys {
			if _, ok := info.Env[key]; !ok {
				t.Errorf("missing env key %q", key)
			}
		}
	})

	t.Run("command points to app binary", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		info, err := rm.GetFNMCommandInfo("mmdc", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo() error = %v", err)
		}

		if filepath.Base(info.Command) != "mmdc" {
			t.Errorf("command should be app binary, got %q", filepath.Base(info.Command))
		}
	})

	t.Run("PATH includes node binary directory", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		info, err := rm.GetFNMCommandInfo("mmdc", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo() error = %v", err)
		}

		pathVal, ok := info.Env["PATH"]
		if !ok {
			t.Fatal("PATH not set in env")
		}
		if !strings.Contains(pathVal, "fnm-nodes") {
			t.Errorf("PATH should include fnm-nodes directory, got %q", pathVal)
		}
	})

	t.Run("invalid runtime returns error", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "nonexistent",
		}

		_, err := rm.GetFNMCommandInfo("mmdc", appConfig, nil, nil)
		if err == nil {
			t.Error("expected error for nonexistent runtime, got nil")
		}
	})

	t.Run("deterministic paths", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		info1, err := rm.GetFNMCommandInfo("mmdc", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}

		info2, err := rm.GetFNMCommandInfo("mmdc", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}

		if info1.Command != info2.Command {
			t.Errorf("commands not deterministic: %q != %q", info1.Command, info2.Command)
		}
	})

	t.Run("different versions produce different paths", func(t *testing.T) {
		config1 := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}
		config2 := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "12.0.0",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		info1, err := rm.GetFNMCommandInfo("mmdc", config1, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		info2, err := rm.GetFNMCommandInfo("mmdc", config2, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}

		if info1.Command == info2.Command {
			t.Error("different versions should produce different app paths")
		}
	})

	t.Run("different node versions produce different paths", func(t *testing.T) {
		// Node version is now on the runtime, so create runtimes with different node versions
		runtimesWithAltNode := makeFNMTestRuntimes()
		runtimesWithAltNode["fnm-alt-node"] = config.RuntimeConfig{
			Kind: config.RuntimeKindFNM,
			Mode: config.RuntimeModeManaged,
			FNM: &config.RuntimeConfigFNM{
				NodeVersion: "22.0.0",
				PNPMVersion: "9.15.0",
				PNPMHash:    "test-pnpm-sha256-hash",
			},
			Managed: runtimesWithAltNode["fnm"].Managed,
		}
		rmAlt := New(runtimesWithAltNode)

		appConfig1 := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",
			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}
		appConfig2 := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",
			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm-alt-node",
		}

		info1, err := rmAlt.GetFNMCommandInfo("mmdc", appConfig1, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		info2, err := rmAlt.GetFNMCommandInfo("mmdc", appConfig2, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}

		if info1.Command == info2.Command {
			t.Error("different node versions should produce different app env paths")
		}
		if info1.Env["PATH"] == info2.Env["PATH"] {
			t.Error("different node versions should produce different PATH values")
		}
	})

	t.Run("dependencies affect app paths", func(t *testing.T) {
		config1 := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}
		config2 := &binmanager.AppConfigFNM{
			PackageName:  "@mermaid-js/mermaid-cli",
			Version:      "11.4.2",

			BinPath:      "node_modules/.bin/mmdc",
			Runtime:      "fnm",
			Dependencies: map[string]string{"puppeteer": "21.0.0"},
		}

		info1, err := rm.GetFNMCommandInfo("mmdc", config1, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		info2, err := rm.GetFNMCommandInfo("mmdc", config2, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}

		if info1.Command == info2.Command {
			t.Error("dependencies should produce different app paths")
		}
	})

	t.Run("system runtime works", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm-system",
		}

		info, err := rm.GetFNMCommandInfo("mmdc", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("GetFNMCommandInfo() error = %v", err)
		}

		if info.Type != "fnm" {
			t.Errorf("Type = %q, want %q", info.Type, "fnm")
		}
	})
}

func TestResolveFNMAppEnvPath(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	t.Run("returns non-empty path", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		path, runtimeName, _, err := rm.resolveFNMAppEnvPath("mmdc", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("resolveFNMAppEnvPath() error = %v", err)
		}
		if path == "" {
			t.Error("path is empty")
		}
		if runtimeName != "fnm" {
			t.Errorf("runtimeName = %q, want %q", runtimeName, "fnm")
		}
	})

	t.Run("deterministic path", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		path1, _, _, _ := rm.resolveFNMAppEnvPath("mmdc", appConfig, nil, nil)
		path2, _, _, _ := rm.resolveFNMAppEnvPath("mmdc", appConfig, nil, nil)

		if path1 != path2 {
			t.Errorf("paths not deterministic: %q != %q", path1, path2)
		}
	})

	t.Run("invalid runtime", func(t *testing.T) {
		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",

			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "nonexistent",
		}

		_, _, _, err := rm.resolveFNMAppEnvPath("mmdc", appConfig, nil, nil)
		if err == nil {
			t.Error("expected error for nonexistent runtime")
		}
	})
}

func TestGetFNMCommandInfo_LockFileAffectsPath(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	configNoLock := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",

		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
	}
	configWithLock := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",

		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
		LockFile:    "lockfileVersion: '9.0'\npackages:\n  example@1.0.0:\n    resolution: {integrity: sha512-abc}",
	}

	infoNoLock, err := rm.GetFNMCommandInfo("mmdc", configNoLock, nil, nil)
	if err != nil {
		t.Fatalf("GetFNMCommandInfo() without lock error = %v", err)
	}
	infoWithLock, err := rm.GetFNMCommandInfo("mmdc", configWithLock, nil, nil)
	if err != nil {
		t.Fatalf("GetFNMCommandInfo() with lock error = %v", err)
	}

	if infoNoLock.Command == infoWithLock.Command {
		t.Error("lockFile should produce a different cache path")
	}
}

func TestGetFNMCommandInfo_DifferentLockFilesProduceDifferentPaths(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	config1 := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",

		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
		LockFile:    "lockfile-content-v1",
	}
	config2 := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",

		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
		LockFile:    "lockfile-content-v2",
	}

	info1, err := rm.GetFNMCommandInfo("mmdc", config1, nil, nil)
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}
	info2, err := rm.GetFNMCommandInfo("mmdc", config2, nil, nil)
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}

	if info1.Command == info2.Command {
		t.Error("different lockFile contents should produce different paths")
	}
}

func TestResolveFNMAppEnvPath_LockFileAffectsPath(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	configNoLock := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",

		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
	}
	configWithLock := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",

		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
		LockFile:    "some lock file content",
	}

	path1, _, _, err := rm.resolveFNMAppEnvPath("mmdc", configNoLock, nil, nil)
	if err != nil {
		t.Fatalf("resolveFNMAppEnvPath() without lock error = %v", err)
	}
	path2, _, _, err := rm.resolveFNMAppEnvPath("mmdc", configWithLock, nil, nil)
	if err != nil {
		t.Fatalf("resolveFNMAppEnvPath() with lock error = %v", err)
	}

	if path1 == path2 {
		t.Error("lockFile should change the resolved app env path")
	}
}

func TestInstallFNMAppAlreadyInstalled(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	appConfig := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",

		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
	}

	appEnvPath, _, _, err := rm.resolveFNMAppEnvPath("mmdc", appConfig, nil, nil)
	if err != nil {
		t.Fatalf("resolveFNMAppEnvPath() error = %v", err)
	}

	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)
	if err := os.MkdirAll(filepath.Dir(appBinPath), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(appBinPath, []byte("#!/bin/sh\necho ok"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	// Also create the module package.json so the integrity check passes
	appModuleDir := filepath.Join(appEnvPath, "node_modules", appConfig.PackageName)
	if err := os.MkdirAll(appModuleDir, 0755); err != nil {
		t.Fatalf("failed to create module dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appModuleDir, "package.json"), []byte(`{"name":"`+appConfig.PackageName+`"}`), 0644); err != nil {
		t.Fatalf("failed to write module package.json: %v", err)
	}
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	err = rm.InstallFNMApp("mmdc", appConfig, nil, nil)
	if err != nil {
		t.Errorf("InstallFNMApp() error = %v, expected nil for already installed app", err)
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

// downloadPNPMFromRegistryWithURL is a test helper that allows injecting a custom registry URL.
// It computes the SHA-256 hash of the tarball data from the server and passes it as the pinned hash,
// simulating a correctly configured pnpmHash in the config.
func downloadPNPMFromRegistryWithURL(registryURL string, version string, destDir string) error {
	pnpmCjsPath := filepath.Join(destDir, "package", "bin", "pnpm.cjs")
	if _, err := os.Stat(pnpmCjsPath); err == nil {
		return nil
	}

	url := fmt.Sprintf("%s/pnpm/%s", registryURL, version)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch PNPM metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("npm registry returned status %d for pnpm@%s", resp.StatusCode, version)
	}

	var meta npmVersionMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return fmt.Errorf("failed to decode PNPM metadata: %w", err)
	}

	if meta.Dist.Tarball == "" {
		return fmt.Errorf("no tarball URL found for pnpm@%s", version)
	}

	if meta.Dist.Integrity == "" || !strings.HasPrefix(meta.Dist.Integrity, "sha512-") {
		return fmt.Errorf("pnpm@%s: SHA-512 integrity required but not found in registry metadata", version)
	}

	tarResp, err := http.Get(meta.Dist.Tarball)
	if err != nil {
		return fmt.Errorf("failed to download PNPM tarball: %w", err)
	}
	defer func() { _ = tarResp.Body.Close() }()

	if tarResp.StatusCode != http.StatusOK {
		return fmt.Errorf("pnpm tarball download returned status %d", tarResp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "pnpm-*.tgz")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	sha256Hasher := sha256.New()
	sha512Hasher := sha512.New()
	writer := io.MultiWriter(tmpFile, sha256Hasher, sha512Hasher)

	if _, err := io.Copy(writer, tarResp.Body); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to download PNPM tarball: %w", err)
	}
	_ = tmpFile.Close()

	pnpmHash := hex.EncodeToString(sha256Hasher.Sum(nil))
	if err := verifyPNPMPinnedHash(pnpmHash, sha256Hasher.Sum(nil)); err != nil {
		return err
	}

	if err := verifyPNPMIntegrity(meta, sha512Hasher.Sum(nil)); err != nil {
		return err
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create PNPM directory: %w", err)
	}

	if err := extractFullTgz(tmpPath, destDir); err != nil {
		return fmt.Errorf("failed to extract PNPM tarball: %w", err)
	}

	return nil
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

func TestWriteFNMAppWorkspaceFile(t *testing.T) {
	t.Run("nil files writes defaults to disk", func(t *testing.T) {
		appEnvPath := t.TempDir()

		filtered, err := writeFNMAppWorkspaceFile(appEnvPath, nil)
		if err != nil {
			t.Fatalf("writeFNMAppWorkspaceFile() error = %v", err)
		}
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

		filtered, err := writeFNMAppWorkspaceFile(appEnvPath, files)
		if err != nil {
			t.Fatalf("writeFNMAppWorkspaceFile() error = %v", err)
		}
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

		filtered, err := writeFNMAppWorkspaceFile(appEnvPath, files)
		if err != nil {
			t.Fatalf("writeFNMAppWorkspaceFile() error = %v", err)
		}

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

		if _, err := writeFNMAppWorkspaceFile(appEnvPath, nil); err != nil {
			t.Fatalf("writeFNMAppWorkspaceFile() error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(appEnvPath, "pnpm-workspace.yaml")); err != nil {
			t.Errorf("workspace file not created: %v", err)
		}
	})

	t.Run("invalid user YAML returns error", func(t *testing.T) {
		appEnvPath := t.TempDir()
		files := map[string]string{
			"pnpm-workspace.yaml": "not: valid: yaml: at: all: [",
		}

		_, err := writeFNMAppWorkspaceFile(appEnvPath, files)
		if err == nil {
			t.Error("expected error for invalid user YAML, got nil")
		}
	})
}

// TestWriteFNMAppWorkspaceFile_LockfileRegenerationScenario simulates the
// `datamitsu config lockfile <app>` flow where the install runs with an
// empty LockFile but the user's files["pnpm-workspace.yaml"] is still
// expected to be merged with the secure defaults. This guards against
// regressions where lockfile regeneration would skip the workspace merge
// (e.g., if defaults were only written when a lockfile was already present).
func TestWriteFNMAppWorkspaceFile_LockfileRegenerationScenario(t *testing.T) {
	runtimes := makeFNMTestRuntimes()
	rm := New(runtimes)

	appConfig := &binmanager.AppConfigFNM{
		PackageName: "@mermaid-js/mermaid-cli",
		Version:     "11.4.2",
		BinPath:     "node_modules/.bin/mmdc",
		Runtime:     "fnm",
	}

	files := map[string]string{
		"pnpm-workspace.yaml": "allowBuilds:\n  puppeteer: true\n",
	}

	appEnvPath, _, _, err := rm.resolveFNMAppEnvPath("mmdc-lockfile-regeneration", appConfig, files, nil)
	if err != nil {
		t.Fatalf("resolveFNMAppEnvPath() error = %v", err)
	}
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	filtered, err := writeFNMAppWorkspaceFile(appEnvPath, files)
	if err != nil {
		t.Fatalf("writeFNMAppWorkspaceFile() error = %v", err)
	}

	if _, has := filtered["pnpm-workspace.yaml"]; has {
		t.Error("filtered files must not include pnpm-workspace.yaml; the merge consumes it")
	}

	content, err := os.ReadFile(filepath.Join(appEnvPath, "pnpm-workspace.yaml"))
	if err != nil {
		t.Fatalf("workspace file not written during lockfile regeneration: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("failed to parse workspace yaml: %v", err)
	}

	if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
		t.Errorf("strictDepBuilds = %v, want true (secure default must still apply during lockfile regeneration)", parsed["strictDepBuilds"])
	}
	if !pnpmWorkspaceValueEqual(parsed["blockExoticSubdeps"], true) {
		t.Errorf("blockExoticSubdeps = %v, want true (secure default must still apply during lockfile regeneration)", parsed["blockExoticSubdeps"])
	}
	if !pnpmWorkspaceValueEqual(parsed["minimumReleaseAge"], 10080) {
		t.Errorf("minimumReleaseAge = %v, want 10080 (secure default must still apply during lockfile regeneration)", parsed["minimumReleaseAge"])
	}

	allowBuilds, ok := parsed["allowBuilds"]
	if !ok {
		t.Fatal("merged workspace yaml missing user's allowBuilds entry; lockfile regeneration would fail for packages with build scripts")
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
}

func TestInstallFNMAppOnceWritesWorkspaceFile(t *testing.T) {
	t.Run("writes defaults when no user override", func(t *testing.T) {
		runtimes := makeFNMTestRuntimes()
		rm := New(runtimes)

		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",
			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		appEnvPath, _, _, err := rm.resolveFNMAppEnvPath("mmdc-defaults", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("resolveFNMAppEnvPath() error = %v", err)
		}

		filtered, err := writeFNMAppWorkspaceFile(appEnvPath, nil)
		if err != nil {
			t.Fatalf("writeFNMAppWorkspaceFile() error = %v", err)
		}
		defer func() { _ = os.RemoveAll(appEnvPath) }()

		if filtered != nil {
			t.Errorf("filtered = %v, want nil", filtered)
		}

		content, err := os.ReadFile(filepath.Join(appEnvPath, "pnpm-workspace.yaml"))
		if err != nil {
			t.Fatalf("workspace file not found: %v", err)
		}
		var parsed map[string]any
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to parse: %v", err)
		}
		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true", parsed["strictDepBuilds"])
		}
		if !pnpmWorkspaceValueEqual(parsed["blockExoticSubdeps"], true) {
			t.Errorf("blockExoticSubdeps = %v, want true", parsed["blockExoticSubdeps"])
		}
	})

	t.Run("writes merged result when user provides workspace yaml", func(t *testing.T) {
		runtimes := makeFNMTestRuntimes()
		rm := New(runtimes)

		appConfig := &binmanager.AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.4.2",
			BinPath:     "node_modules/.bin/mmdc",
			Runtime:     "fnm",
		}

		files := map[string]string{
			"pnpm-workspace.yaml": "allowBuilds:\n  puppeteer: true\n",
		}

		appEnvPath, _, _, err := rm.resolveFNMAppEnvPath("mmdc-merged", appConfig, files, nil)
		if err != nil {
			t.Fatalf("resolveFNMAppEnvPath() error = %v", err)
		}

		filtered, err := writeFNMAppWorkspaceFile(appEnvPath, files)
		if err != nil {
			t.Fatalf("writeFNMAppWorkspaceFile() error = %v", err)
		}
		defer func() { _ = os.RemoveAll(appEnvPath) }()

		if _, has := filtered["pnpm-workspace.yaml"]; has {
			t.Error("filtered files should not include pnpm-workspace.yaml")
		}

		content, err := os.ReadFile(filepath.Join(appEnvPath, "pnpm-workspace.yaml"))
		if err != nil {
			t.Fatalf("workspace file not found: %v", err)
		}
		var parsed map[string]any
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to parse: %v", err)
		}

		if !pnpmWorkspaceValueEqual(parsed["strictDepBuilds"], true) {
			t.Errorf("strictDepBuilds = %v, want true (default)", parsed["strictDepBuilds"])
		}

		allowBuilds, ok := parsed["allowBuilds"]
		if !ok {
			t.Fatal("missing allowBuilds in merged output")
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
}
