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
	t.Run("without lockfile includes --silent", func(t *testing.T) {
		args := buildPNPMInstallArgs("/path/to/pnpm.cjs", false)
		expected := []string{"/path/to/pnpm.cjs", "install", "--silent"}
		if len(args) != len(expected) {
			t.Fatalf("args length = %d, want %d", len(args), len(expected))
		}
		for i, arg := range args {
			if arg != expected[i] {
				t.Errorf("args[%d] = %q, want %q", i, arg, expected[i])
			}
		}
	})

	t.Run("with lockfile includes --silent and --frozen-lockfile", func(t *testing.T) {
		args := buildPNPMInstallArgs("/path/to/pnpm.cjs", true)
		expected := []string{"/path/to/pnpm.cjs", "install", "--silent", "--frozen-lockfile"}
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
