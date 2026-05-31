package runtimemanager

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/ulikunitz/xz"
)

// nodeStrPtr returns a pointer to s (BinaryOsArchInfo.BinaryPath is *string).
func nodeStrPtr(s string) *string { return &s }

// the archive's single top-level directory; binaryPath into it resolves node.
const nodeArchiveTopDir = "node-v26.2.0-linux-x64"
const nodeArchiveBinaryPath = nodeArchiveTopDir + "/bin/node"
const nodeStubContent = "#!/bin/sh\necho node-archive\n"

// makeNodeTarXzBytes builds an in-memory .tar.xz mirroring a real node release
// layout (node-vX-os-arch/bin/node) and returns the bytes plus their SHA-256
// hex digest for use as the registry's pinned hash.
func makeNodeTarXzBytes(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	xzWriter, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("failed to create xz writer: %v", err)
	}
	tarWriter := tar.NewWriter(xzWriter)

	if err := tarWriter.WriteHeader(&tar.Header{Name: nodeArchiveTopDir + "/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: nodeArchiveTopDir + "/bin/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatalf("write bin dir header: %v", err)
	}
	content := []byte(nodeStubContent)
	if err := tarWriter.WriteHeader(&tar.Header{Name: nodeArchiveBinaryPath, Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatalf("write node header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("write node content: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := xzWriter.Close(); err != nil {
		t.Fatalf("close xz writer: %v", err)
	}

	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// nodeRuntimeWith builds a single managed "node" runtime whose binaries map has
// one entry per libcKey under the host's os/arch, all pointing at url with the
// pinned hash. Used to exercise libc selection (musl) and glibc fallback.
func nodeRuntimeWith(t *testing.T, url, hash string, libcKeys ...string) config.MapOfRuntimes {
	t.Helper()
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("detect os type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("detect arch type: %v", err)
	}

	libcMap := map[string]binmanager.BinaryOsArchInfo{}
	for _, k := range libcKeys {
		libcMap[k] = binmanager.BinaryOsArchInfo{
			URL:         url,
			Hash:        hash,
			ContentType: binmanager.BinContentTypeTarXz,
			BinaryPath:  nodeStrPtr(nodeArchiveBinaryPath),
			ExtractDir:  true,
		}
	}

	return config.MapOfRuntimes{
		"node": {
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{
				NodeVersion: "26.2.0",
				PNPMVersion: "11.0.0",
				PNPMHash:    "0000000000000000000000000000000000000000000000000000000000000000",
			},
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{osType: {archType: libcMap}},
			},
		},
	}
}

// nodeArchiveServer serves the given tar.xz bytes for any path that matches
// allowPath; every other path returns 404. It counts the number of archive
// (200) responses so tests can assert cache hits avoid re-downloading.
func nodeArchiveServer(t *testing.T, body []byte, allowPath string, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowPath != "" && r.URL.Path != allowPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/x-xz")
		_, _ = w.Write(body)
	}))
}

func TestGetNodeEnvVars(t *testing.T) {
	appEnvPath := "/cache/.apps/node/eslint/abc123"
	vars := getNodeEnvVars(appEnvPath)

	if vars["npm_config_virtual_store_dir"] != filepath.Join(appEnvPath, "node_modules", ".pnpm") {
		t.Errorf("npm_config_virtual_store_dir = %q", vars["npm_config_virtual_store_dir"])
	}
	if vars["npm_config_global_dir"] != filepath.Join(appEnvPath, "global") {
		t.Errorf("npm_config_global_dir = %q", vars["npm_config_global_dir"])
	}
	if vars["npm_config_store_dir"] == "" {
		t.Error("npm_config_store_dir should be set")
	}
}

func TestInstallNode_DownloadVerifyExtract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("node stub is a /bin/sh script; skip on Windows")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	body, hash := makeNodeTarXzBytes(t)
	var hits int32
	server := nodeArchiveServer(t, body, "/node.tar.xz", &hits)
	defer server.Close()

	rm := New(nodeRuntimeWith(t, server.URL+"/node.tar.xz", hash, testLibc))

	nodeBin, err := rm.installNode("node")
	if err != nil {
		t.Fatalf("installNode() error = %v", err)
	}
	if filepath.Base(nodeBin) != "node" {
		t.Errorf("resolved binary %q should be named node", nodeBin)
	}
	info, err := os.Stat(nodeBin)
	if err != nil {
		t.Fatalf("node binary missing after install: %v", err)
	}
	if info.Mode()&0100 == 0 {
		t.Error("node binary should be executable")
	}
	got, err := os.ReadFile(nodeBin)
	if err != nil {
		t.Fatalf("read node binary: %v", err)
	}
	if string(got) != nodeStubContent {
		t.Errorf("node binary content = %q, want %q", string(got), nodeStubContent)
	}
}

func TestInstallNode_SHA256Mismatch(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	body, _ := makeNodeTarXzBytes(t)
	var hits int32
	server := nodeArchiveServer(t, body, "/node.tar.xz", &hits)
	defer server.Close()

	// Pin a hash that does not match the served bytes: the download must be
	// rejected per the mandatory hash-verification policy.
	const wrongHash = "1111111111111111111111111111111111111111111111111111111111111111"
	rm := New(nodeRuntimeWith(t, server.URL+"/node.tar.xz", wrongHash, testLibc))

	nodeBin, err := rm.installNode("node")
	if err == nil {
		t.Fatalf("installNode() expected hash-mismatch error, got nil (path %q)", nodeBin)
	}
}

func TestInstallNode_CacheHitNoRefetch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("node stub is a /bin/sh script; skip on Windows")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	body, hash := makeNodeTarXzBytes(t)
	var hits int32
	server := nodeArchiveServer(t, body, "/node.tar.xz", &hits)
	defer server.Close()

	rm := New(nodeRuntimeWith(t, server.URL+"/node.tar.xz", hash, testLibc))

	if _, err := rm.installNode("node"); err != nil {
		t.Fatalf("first installNode() error = %v", err)
	}
	first := atomic.LoadInt32(&hits)
	if first == 0 {
		t.Fatal("expected the archive to be downloaded at least once")
	}

	if _, err := rm.installNode("node"); err != nil {
		t.Fatalf("second installNode() error = %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != first {
		t.Errorf("expected cache hit (no re-download); hits went %d -> %d", first, got)
	}
}

// TestNodeLibcSelection covers the libc resolution node relies on (resolveLibcKey,
// the same selector GetRuntimePath uses): a musl host with a musl entry picks
// musl, and a musl host with no musl entry falls back to the glibc archive. This
// is host-independent — unlike a full install, which lets binmanager re-detect
// the real host libc and so cannot exercise musl selection on a glibc CI host.
func TestNodeLibcSelection(t *testing.T) {
	musl := binmanager.BinaryOsArchInfo{
		URL: "https://unofficial-builds.nodejs.org/node-musl.tar.xz", Hash: "musl",
		ContentType: binmanager.BinContentTypeTarXz, BinaryPath: nodeStrPtr(nodeArchiveBinaryPath), ExtractDir: true,
	}
	glibc := binmanager.BinaryOsArchInfo{
		URL: "https://nodejs.org/dist/node.tar.xz", Hash: "glibc",
		ContentType: binmanager.BinContentTypeTarXz, BinaryPath: nodeStrPtr(nodeArchiveBinaryPath), ExtractDir: true,
	}

	t.Run("musl host selects musl entry", func(t *testing.T) {
		libcMap := map[string]binmanager.BinaryOsArchInfo{"glibc": glibc, "musl": musl}
		info, resolved := resolveLibcKey(libcMap, "musl")
		if info == nil {
			t.Fatal("expected a musl entry, got nil")
		}
		if resolved != "musl" || info.Hash != "musl" {
			t.Errorf("selected libc=%q hash=%q, want musl/musl", resolved, info.Hash)
		}
	})

	t.Run("musl host falls back to glibc when no musl entry", func(t *testing.T) {
		libcMap := map[string]binmanager.BinaryOsArchInfo{"glibc": glibc}
		info, resolved := resolveLibcKey(libcMap, "musl")
		if info == nil {
			t.Fatal("expected glibc fallback, got nil")
		}
		if resolved != "glibc" || info.Hash != "glibc" {
			t.Errorf("fallback libc=%q hash=%q, want glibc/glibc", resolved, info.Hash)
		}
	})
}

func TestInstallNode_GlibcFallbackWhenNoMusl(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("node stub is a /bin/sh script; skip on Windows")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	body, hash := makeNodeTarXzBytes(t)
	var hits int32
	server := nodeArchiveServer(t, body, "/glibc.tar.xz", &hits)
	defer server.Close()

	// Only a glibc entry exists; on a musl host with no musl entry and no system
	// node fallback (kind "node" has no system command yet), resolveLibcKey must
	// fall back to the glibc archive.
	runtimes := nodeRuntimeWith(t, server.URL+"/glibc.tar.xz", hash, "glibc")
	rm := newTestRMWithTarget(runtimes, target.Target{OS: runtime.GOOS, Arch: runtime.GOARCH, Libc: target.LibcMusl})

	nodeBin, err := rm.installNode("node")
	if err != nil {
		t.Fatalf("installNode() error = %v (glibc fallback expected)", err)
	}
	if _, err := os.Stat(nodeBin); err != nil {
		t.Fatalf("node binary missing: %v", err)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Error("expected the glibc archive to be downloaded")
	}
}

func TestInstallNode_UnknownRuntime(t *testing.T) {
	rm := New(config.MapOfRuntimes{})
	if _, err := rm.installNode("node"); err == nil {
		t.Error("expected error for unknown runtime, got nil")
	}
}

func TestInstallNodeApp_InvalidRuntime(t *testing.T) {
	rm := New(config.MapOfRuntimes{})
	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "nonexistent",
	}
	if err := rm.InstallNodeApp("eslint", appConfig, nil, nil); err == nil {
		t.Error("expected error for nonexistent runtime, got nil")
	}
}

func TestGetNodeCommandInfo_InvalidRuntime(t *testing.T) {
	rm := New(config.MapOfRuntimes{})
	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "nonexistent",
	}
	if _, err := rm.GetNodeCommandInfo("eslint", appConfig, nil, nil); err == nil {
		t.Error("expected error for nonexistent runtime, got nil")
	}
}

func TestGetNodeCommandInfo_MissingNodeConfig(t *testing.T) {
	// A node-kind runtime with managed binaries but no node config must be
	// rejected with a clear error rather than panicking on rc.Node.
	osType, _ := syslist.GetOsTypeFromString(runtime.GOOS)
	archType, _ := syslist.GetArchTypeFromString(runtime.GOARCH)
	runtimes := config.MapOfRuntimes{
		"node": {
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					osType: {
						archType: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/node.tar.xz",
							Hash:        "abc",
							ContentType: binmanager.BinContentTypeTarXz,
							BinaryPath:  nodeStrPtr(nodeArchiveBinaryPath),
							ExtractDir:  true,
						}},
					},
				},
			},
		},
	}
	rm := New(runtimes)
	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}
	if _, err := rm.GetNodeCommandInfo("eslint", appConfig, nil, nil); err == nil {
		t.Error("expected error when runtime has no node config, got nil")
	}
}

func TestInstallNodeApp_AlreadyInstalled(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	osType, _ := syslist.GetOsTypeFromString(runtime.GOOS)
	archType, _ := syslist.GetArchTypeFromString(runtime.GOARCH)
	runtimes := config.MapOfRuntimes{
		"node": {
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{NodeVersion: "26.2.0", PNPMVersion: "11.0.0", PNPMHash: "deadbeef"},
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					osType: {
						archType: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         "https://example.com/node.tar.xz",
							Hash:        "abc",
							ContentType: binmanager.BinContentTypeTarXz,
							BinaryPath:  nodeStrPtr(nodeArchiveBinaryPath),
							ExtractDir:  true,
						}},
					},
				},
			},
		},
	}
	rm := New(runtimes)

	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}

	appEnvPath, _, _, err := rm.resolveNodeAppEnvPath("eslint", appConfig, nil, nil)
	if err != nil {
		t.Fatalf("resolveNodeAppEnvPath() error = %v", err)
	}

	// Pre-create the bin shim and the installed module's package.json so the
	// install short-circuits without touching the network/runtime.
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)
	if err := os.MkdirAll(filepath.Dir(appBinPath), 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.WriteFile(appBinPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write bin shim: %v", err)
	}
	modulePkg := filepath.Join(appEnvPath, "node_modules", appConfig.PackageName, "package.json")
	if err := os.MkdirAll(filepath.Dir(modulePkg), 0755); err != nil {
		t.Fatalf("mkdir module dir: %v", err)
	}
	if err := os.WriteFile(modulePkg, []byte("{}"), 0644); err != nil {
		t.Fatalf("write module package.json: %v", err)
	}
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	if err := rm.InstallNodeApp("eslint", appConfig, nil, nil); err != nil {
		t.Errorf("InstallNodeApp() error = %v, expected nil for already-installed app", err)
	}
}
