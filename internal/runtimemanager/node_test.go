package runtimemanager

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// TestSystemCommandForKindNode verifies the node kind reports "node" as its
// system fallback command (used by resolveEffectiveRuntimeConfig when a musl
// host lacks a musl archive). Additive alongside the existing uv/jvm/go arms.
func TestSystemCommandForKindNode(t *testing.T) {
	if got := systemCommandForKind(config.RuntimeKindNode); got != "node" {
		t.Errorf("systemCommandForKind(node) = %q, want %q", got, "node")
	}
}

// TestComputeAppPathNode exercises the node arm of ComputeAppPath: a node app
// resolves to a deterministic, version-sensitive cache path under the "node"
// kind directory without installing anything.
func TestComputeAppPathNode(t *testing.T) {
	runtimes := nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc)
	rm := New(runtimes)

	nodeApp := func(version string) binmanager.App {
		return binmanager.App{
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     version,
				BinPath:     "node_modules/.bin/eslint",
				Runtime:     "node",
			},
		}
	}

	t.Run("node app computes path", func(t *testing.T) {
		path, err := rm.ComputeAppPath("eslint", nodeApp("9.0.0"))
		if err != nil {
			t.Fatalf("ComputeAppPath() error = %v", err)
		}
		if path == "" {
			t.Error("path is empty")
		}
	})

	t.Run("node app path is deterministic", func(t *testing.T) {
		p1, _ := rm.ComputeAppPath("eslint", nodeApp("9.0.0"))
		p2, _ := rm.ComputeAppPath("eslint", nodeApp("9.0.0"))
		if p1 != p2 {
			t.Errorf("path not deterministic: %q != %q", p1, p2)
		}
	})

	t.Run("different versions produce different paths", func(t *testing.T) {
		p1, _ := rm.ComputeAppPath("eslint", nodeApp("9.0.0"))
		p2, _ := rm.ComputeAppPath("eslint", nodeApp("9.1.0"))
		if p1 == p2 {
			t.Error("different versions should produce different paths")
		}
	})

	t.Run("path lives under the node kind directory", func(t *testing.T) {
		path, err := rm.ComputeAppPath("eslint", nodeApp("9.0.0"))
		if err != nil {
			t.Fatalf("ComputeAppPath() error = %v", err)
		}
		if !strings.Contains(path, filepath.Join("node", "eslint")) {
			t.Errorf("node app path %q should live under node/eslint", path)
		}
	})
}

// TestGetCommandInfoNode verifies the node arm of GetCommandInfo dispatches to
// the node install/command flow. The install fails (no real archive at the fake
// URL), but the dispatch must recognize the node app as runtime-managed rather
// than rejecting it as having no valid configuration.
func TestGetCommandInfoNode(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	runtimes := nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc)
	rm := New(runtimes)

	app := binmanager.App{
		Node: &binmanager.AppConfigNode{
			PackageName: "eslint",
			Version:     "9.0.0",
			BinPath:     "node_modules/.bin/eslint",
			Runtime:     "node",
		},
	}

	_, err := rm.GetCommandInfo("eslint", app)
	if err == nil {
		t.Fatal("expected an error (node archive is not reachable at the fake URL), got nil")
	}
	// Recognized as runtime-managed AND routed to the node install path: the
	// failure originates from installNode wrapping the unreachable archive fetch
	// ("failed to acquire node runtime"). Dispatch to the wrong kind (jvm/uv) or
	// a fall-through to "not a runtime-managed app" would not mention the node runtime.
	if !strings.Contains(err.Error(), "node runtime") {
		t.Errorf("expected node-dispatch error from the node runtime install path, got: %v", err)
	}
}

// TestCollectRequiredRuntimesNode verifies the node arm of CollectRequiredRuntimes:
// required node apps pull in their node runtime (explicit ref or default-by-kind),
// optional apps are excluded, and node sorts alongside other kinds.
func TestCollectRequiredRuntimesNode(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv": {
			Kind: config.RuntimeKindUV,
			Mode: config.RuntimeModeManaged,
		},
		"node": {
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{NodeVersion: "26.2.0", PNPMVersion: "11.0.0", PNPMHash: "h"},
		},
	}

	nodeApp := func(required bool, runtimeRef string) binmanager.App {
		return binmanager.App{
			Required: required,
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "9.0.0",
				BinPath:     "node_modules/.bin/eslint",
				Runtime:     runtimeRef,
			},
		}
	}

	t.Run("required node app collects default node runtime", func(t *testing.T) {
		apps := binmanager.MapOfApps{"eslint": nodeApp(true, "")}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 || result[0] != "node" {
			t.Fatalf("expected [node], got %v", result)
		}
	})

	t.Run("node app with explicit runtime ref", func(t *testing.T) {
		apps := binmanager.MapOfApps{"eslint": nodeApp(true, "node")}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 1 || result[0] != "node" {
			t.Fatalf("expected [node], got %v", result)
		}
	})

	t.Run("optional node app excluded", func(t *testing.T) {
		apps := binmanager.MapOfApps{"eslint": nodeApp(false, "")}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 0 {
			t.Errorf("expected 0 runtimes for optional node app, got %v", result)
		}
	})

	t.Run("mixed uv and node apps", func(t *testing.T) {
		apps := binmanager.MapOfApps{
			"yamllint": {Required: true, Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.37.0", Runtime: "uv"}},
			"eslint":   nodeApp(true, "node"),
		}
		result := CollectRequiredRuntimes(apps, runtimes, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 runtimes, got %v", result)
		}
		if result[0] != "node" || result[1] != "uv" {
			t.Errorf("expected sorted [node uv], got %v", result)
		}
	})
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
	// The archive must actually be fetched and hashed before being rejected —
	// the mismatch is detected on the downloaded bytes, not short-circuited.
	if atomic.LoadInt32(&hits) == 0 {
		t.Error("expected the archive to be downloaded before the hash check rejected it")
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

	// Only a glibc entry exists. node now reports "node" as its system fallback
	// command, but if no system node is on PATH, resolveEffectiveRuntimeConfig
	// keeps managed mode and resolveLibcKey must fall back to the glibc archive.
	// Inject a lookPath that finds no system node to force the download path
	// (otherwise a CI host with node on PATH would fall back to system mode).
	noSystemNode := func(string) (string, error) { return "", exec.ErrNotFound }
	runtimes := nodeRuntimeWith(t, server.URL+"/glibc.tar.xz", hash, "glibc")
	rm := newTestRMWithLookPath(runtimes, target.Target{OS: runtime.GOOS, Arch: runtime.GOARCH, Libc: target.LibcMusl}, noSystemNode)

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

// nodeReinstallRuntimes builds a node runtime config pointing its archive at url.
// It is shared by the reinstall-branch tests, which only need the runtime to
// resolve and the app env path to compute; the archive is never expected to be a
// real, downloadable node release.
func nodeReinstallRuntimes(t *testing.T, url, hash string) config.MapOfRuntimes {
	t.Helper()
	osType, _ := syslist.GetOsTypeFromString(runtime.GOOS)
	archType, _ := syslist.GetArchTypeFromString(runtime.GOARCH)
	return config.MapOfRuntimes{
		"node": {
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{NodeVersion: "26.2.0", PNPMVersion: "11.0.0", PNPMHash: "deadbeef"},
			Managed: &config.RuntimeConfigManaged{
				Binaries: binmanager.MapOfBinaries{
					osType: {
						archType: {testLibc: binmanager.BinaryOsArchInfo{
							URL:         url,
							Hash:        hash,
							ContentType: binmanager.BinContentTypeTarXz,
							BinaryPath:  nodeStrPtr(nodeArchiveBinaryPath),
							ExtractDir:  true,
						}},
					},
				},
			},
		},
	}
}

// seedStaleNodeApp creates the bin shim but NOT the installed module's
// package.json, which is exactly the "bin shim exists but module missing" state
// that drives installNodeAppOnce into its reinstall (stale-tree removal) branch.
func seedStaleNodeApp(t *testing.T, appEnvPath, binPath string) string {
	t.Helper()
	appBinPath := filepath.Join(appEnvPath, binPath)
	if err := os.MkdirAll(filepath.Dir(appBinPath), 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.WriteFile(appBinPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write bin shim: %v", err)
	}
	return appBinPath
}

// TestInstallNodeApp_RemoveAllFailureAborts pins the review #10 fix: when the
// stale-tree removal in the reinstall branch fails, the install must abort with a
// wrapped error rather than press on over a half-deleted directory.
func TestInstallNodeApp_RemoveAllFailureAborts(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	rm := New(nodeReinstallRuntimes(t, "https://example.com/node.tar.xz", "abc"))
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
	appBinPath := seedStaleNodeApp(t, appEnvPath, appConfig.BinPath)
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	sentinel := errors.New("injected removeAll failure")
	var calls int32
	rm.removeAllFunc = func(string) error {
		atomic.AddInt32(&calls, 1)
		return sentinel
	}

	err = rm.InstallNodeApp("eslint", appConfig, nil, nil)
	if err == nil {
		t.Fatal("expected an error when stale-tree removal fails, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error %v should wrap the injected removal failure", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("removeAll called %d times, want 1", got)
	}
	// Aborted before reinstalling: no network/runtime work happened past the
	// failed removal, so the stale bin shim is still present.
	if _, statErr := os.Stat(appBinPath); statErr != nil {
		t.Errorf("stale bin shim should remain after an aborted removal: %v", statErr)
	}
}

// TestInstallNodeApp_RemoveAllSuccessProceeds is the success-path counterpart:
// when the stale-tree removal succeeds, the reinstall proceeds past it (into the
// runtime download, which then fails on the unreachable archive). The point is
// that removal ran exactly once and the install moved on rather than aborting.
func TestInstallNodeApp_RemoveAllSuccessProceeds(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	// A localhost server that 404s every request: the reinstall proceeds into
	// installNode, which fails fast on the missing archive — no flaky external
	// network and no real pnpm run required.
	var hits int32
	server := nodeArchiveServer(t, []byte("unused"), "/never-matches", &hits)
	defer server.Close()

	rm := New(nodeReinstallRuntimes(t, server.URL+"/node.tar.xz", strings.Repeat("a", 64)))
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
	appBinPath := seedStaleNodeApp(t, appEnvPath, appConfig.BinPath)
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	var calls int32
	rm.removeAllFunc = func(p string) error {
		atomic.AddInt32(&calls, 1)
		return os.RemoveAll(p)
	}

	err = rm.InstallNodeApp("eslint", appConfig, nil, nil)
	if err == nil {
		t.Fatal("expected a download error after the (successful) stale-tree removal, got nil")
	}
	// The error is from the runtime download, NOT the removal: we proceeded past
	// the (successful) removal into installNode, which wraps the failed archive
	// fetch from the 404ing server as "failed to acquire node runtime".
	if !strings.Contains(err.Error(), "node runtime") {
		t.Errorf("expected the post-removal error to come from the node runtime download, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("removeAll called %d times, want 1", got)
	}
	// Removal succeeded and the failed reinstall never recreated the tree.
	if _, statErr := os.Stat(appBinPath); !os.IsNotExist(statErr) {
		t.Errorf("stale bin shim should have been removed, stat err = %v", statErr)
	}
}

// seedInstalledNodeApp creates BOTH the bin shim and the installed module's
// package.json — the "already installed" state that makes installNodeAppOnce
// short-circuit and lets GetNodeCommandInfo return command info.
func seedInstalledNodeApp(t *testing.T, appEnvPath, packageName, binPath string) {
	t.Helper()
	appBinPath := filepath.Join(appEnvPath, binPath)
	if err := os.MkdirAll(filepath.Dir(appBinPath), 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.WriteFile(appBinPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write bin shim: %v", err)
	}
	modulePkg := filepath.Join(appEnvPath, "node_modules", packageName, "package.json")
	if err := os.MkdirAll(filepath.Dir(modulePkg), 0755); err != nil {
		t.Fatalf("mkdir module dir: %v", err)
	}
	if err := os.WriteFile(modulePkg, []byte("{}"), 0644); err != nil {
		t.Fatalf("write module package.json: %v", err)
	}
}

// TestGetCommandInfoNode_MergesWorkspaceOnceOnCacheHit pins Task 9 (review #8): a
// single GetCommandInfo call on an already-installed node app must merge+marshal
// the pnpm-workspace.yaml exactly once, not once per resolveNodeAppEnvPath pass
// (install + command-info). A system-mode node runtime lets the command-info pass
// resolve the node binary without any download.
func TestGetCommandInfoNode_MergesWorkspaceOnceOnCacheHit(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	runtimes := config.MapOfRuntimes{
		"node": {
			Kind:   config.RuntimeKindNode,
			Mode:   config.RuntimeModeSystem,
			Node:   &config.RuntimeConfigNode{NodeVersion: "26.2.0", PNPMVersion: "11.0.0", PNPMHash: "deadbeef"},
			System: &config.RuntimeConfigSystem{Command: filepath.Join(t.TempDir(), "bin", "node")},
		},
	}
	rm := New(runtimes)

	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}
	app := binmanager.App{Node: appConfig}

	appEnvPath, _, _, err := rm.resolveNodeAppEnvPath("eslint", appConfig, app.Files, app.Archives)
	if err != nil {
		t.Fatalf("resolveNodeAppEnvPath() error = %v", err)
	}
	seedInstalledNodeApp(t, appEnvPath, appConfig.PackageName, appConfig.BinPath)
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	// Count merge invocations across the whole GetCommandInfo exec.
	var merges int32
	orig := buildPNPMWorkspace
	buildPNPMWorkspace = func(files map[string]string) (string, error) {
		atomic.AddInt32(&merges, 1)
		return orig(files)
	}
	defer func() { buildPNPMWorkspace = orig }()

	if _, err := rm.GetCommandInfo("eslint", app); err != nil {
		t.Fatalf("GetCommandInfo() error = %v", err)
	}
	if got := atomic.LoadInt32(&merges); got != 1 {
		t.Errorf("pnpm-workspace.yaml merge ran %d times per GetCommandInfo, want 1", got)
	}
}

// invalidWorkspaceFiles is the canonical invalid-user-YAML input that drives
// buildPNPMWorkspace (buildPNPMWorkspaceForApp) into a parse error before any
// runtime resolution or network work. Shared by the workspace-error tests.
func invalidWorkspaceFiles() map[string]string {
	return map[string]string{"pnpm-workspace.yaml": "not: valid: yaml: at: all: ["}
}

// TestResolveNodeAppEnvPath_WorkspaceYAMLError pins the buildPNPMWorkspace
// error branch of resolveNodeAppEnvPath: an invalid user pnpm-workspace.yaml
// must surface as the wrapped "failed to compute pnpm-workspace.yaml" error
// before the runtime is resolved, even with an otherwise-valid node runtime.
func TestResolveNodeAppEnvPath_WorkspaceYAMLError(t *testing.T) {
	rm := New(nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc))
	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}

	_, _, _, err := rm.resolveNodeAppEnvPath("eslint", appConfig, invalidWorkspaceFiles(), nil)
	if err == nil {
		t.Fatal("expected error for invalid pnpm-workspace.yaml, got nil")
	}
	if !strings.Contains(err.Error(), "failed to compute pnpm-workspace.yaml") {
		t.Errorf("error should mention failed to compute pnpm-workspace.yaml, got: %v", err)
	}
}

// TestInstallNodeApp_WorkspaceYAMLError is the InstallNodeApp counterpart:
// the merge runs at the public entry point, so an invalid user
// pnpm-workspace.yaml aborts before any install/network work.
func TestInstallNodeApp_WorkspaceYAMLError(t *testing.T) {
	rm := New(nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc))
	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}

	err := rm.InstallNodeApp("eslint", appConfig, invalidWorkspaceFiles(), nil)
	if err == nil {
		t.Fatal("expected error for invalid pnpm-workspace.yaml, got nil")
	}
	if !strings.Contains(err.Error(), "failed to compute pnpm-workspace.yaml") {
		t.Errorf("error should mention failed to compute pnpm-workspace.yaml, got: %v", err)
	}
}

// TestGetNodeCommandInfo_WorkspaceYAMLError is the GetNodeCommandInfo
// counterpart: an invalid user pnpm-workspace.yaml fails the once-per-exec
// merge before the runtime/command info is resolved.
func TestGetNodeCommandInfo_WorkspaceYAMLError(t *testing.T) {
	rm := New(nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc))
	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}

	_, err := rm.GetNodeCommandInfo("eslint", appConfig, invalidWorkspaceFiles(), nil)
	if err == nil {
		t.Fatal("expected error for invalid pnpm-workspace.yaml, got nil")
	}
	if !strings.Contains(err.Error(), "failed to compute pnpm-workspace.yaml") {
		t.Errorf("error should mention failed to compute pnpm-workspace.yaml, got: %v", err)
	}
}

// TestResolveNodeAppEnvPath_CacheKeyUnchanged proves the once-per-exec refactor
// does not move the app cache key: resolveNodeAppEnvPath must yield the exact path
// the previous logic produced (merge folded into the hash via
// filesWithMergedWorkspaceYAML), for nil, override, and unrelated-files inputs.
func TestResolveNodeAppEnvPath_CacheKeyUnchanged(t *testing.T) {
	runtimes := nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc)
	rm := New(runtimes)
	appConfig := &binmanager.AppConfigNode{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
		Runtime:     "node",
	}

	cases := []struct {
		name  string
		files map[string]string
	}{
		{"nil files", nil},
		{"user workspace override", map[string]string{"pnpm-workspace.yaml": "allowBuilds:\n  puppeteer: true\n"}},
		{"unrelated files", map[string]string{".npmrc": "registry=https://registry.npmjs.org/\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Old computation: merge folded into the hash by filesWithMergedWorkspaceYAML.
			oldFilesForHash, err := filesWithMergedWorkspaceYAML(tc.files)
			if err != nil {
				t.Fatalf("filesWithMergedWorkspaceYAML() error = %v", err)
			}
			wantPath, err := rm.GetAppPath("eslint", config.RuntimeKindNode, appConfig.Version, appConfig.Dependencies, lockFileHash(appConfig.LockFile), oldFilesForHash, nil, "node", NodeAppPathExtra{PackageName: appConfig.PackageName, BinPath: appConfig.BinPath})
			if err != nil {
				t.Fatalf("GetAppPath() error = %v", err)
			}

			gotPath, _, _, err := rm.resolveNodeAppEnvPath("eslint", appConfig, tc.files, nil)
			if err != nil {
				t.Fatalf("resolveNodeAppEnvPath() error = %v", err)
			}

			if gotPath != wantPath {
				t.Errorf("cache key changed: resolveNodeAppEnvPath = %q, want %q", gotPath, wantPath)
			}
		})
	}
}
