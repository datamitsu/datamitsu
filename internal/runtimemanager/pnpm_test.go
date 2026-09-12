package runtimemanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/goccy/go-yaml"
)

// testPNPMRuntimeName is the pnpm runtime Node and Bun fixtures point at.
const testPNPMRuntimeName = "pnpm"

// testPNPMRuntime is a managed pnpm runtime shaped like the entry pull-runtimes
// writes to config/src/runtimes.json.
func testPNPMRuntime() config.RuntimeConfig {
	return pnpmRuntimeWithVersion("12.4.1")
}

func pnpmRuntimeWithVersion(version string) config.RuntimeConfig {
	return config.RuntimeConfig{
		Kind:    config.RuntimeKindPNPM,
		Mode:    config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{Binaries: testPNPMBinaries()},
		PNPM:    &config.RuntimeConfigPNPM{PNPMVersion: version},
	}
}

// systemPNPMRuntime is a pnpm runtime that runs command, the way a user relies
// on the pnpm their host provides.
func systemPNPMRuntime(command string) config.RuntimeConfig {
	return config.RuntimeConfig{
		Kind:   config.RuntimeKindPNPM,
		Mode:   config.RuntimeModeSystem,
		System: &config.RuntimeConfigSystem{Command: command},
		PNPM:   &config.RuntimeConfigPNPM{PNPMVersion: "12.4.1"},
	}
}

// testPNPMBinaries is a realistic pnpm binaries map for runtime fixtures: every
// platform datamitsu pins, each with its own SHA-256, shaped like the entries in
// config/src/runtimes.json.
func testPNPMBinaries() binmanager.MapOfBinaries {
	entry := func(file, binaryPath, hash string, contentType binmanager.BinContentType) binmanager.BinaryOsArchInfo {
		return binmanager.BinaryOsArchInfo{
			URL:         "https://github.com/pnpm/pnpm/releases/download/v12.4.1/" + file,
			Hash:        hash,
			ContentType: contentType,
			BinaryPath:  new(binaryPath),
			ExtractDir:  true,
		}
	}
	tgz, zip := binmanager.BinContentTypeTarGz, binmanager.BinContentTypeZip
	return binmanager.MapOfBinaries{
		syslist.OsTypeDarwin: {
			syslist.ArchTypeAmd64: {"unknown": entry("pnpm-darwin-x64.tar.gz", "pnpm", strings.Repeat("a1", 32), tgz)},
			syslist.ArchTypeArm64: {"unknown": entry("pnpm-darwin-arm64.tar.gz", "pnpm", strings.Repeat("a2", 32), tgz)},
		},
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {
				"glibc": entry("pnpm-linux-x64.tar.gz", "pnpm", strings.Repeat("b1", 32), tgz),
				"musl":  entry("pnpm-linux-x64-musl.tar.gz", "pnpm", strings.Repeat("b2", 32), tgz),
			},
			syslist.ArchTypeArm64: {
				"glibc": entry("pnpm-linux-arm64.tar.gz", "pnpm", strings.Repeat("b3", 32), tgz),
				"musl":  entry("pnpm-linux-arm64-musl.tar.gz", "pnpm", strings.Repeat("b4", 32), tgz),
			},
		},
		syslist.OsTypeWindows: {
			syslist.ArchTypeAmd64: {"unknown": entry("pnpm-win32-x64.zip", "pnpm.exe", strings.Repeat("c1", 32), zip)},
			syslist.ArchTypeArm64: {"unknown": entry("pnpm-win32-arm64.zip", "pnpm.exe", strings.Repeat("c2", 32), zip)},
		},
	}
}

// hostPNPMRuntime is a managed pnpm runtime pinning one archive for this host's
// os/arch under the given libc keys.
func hostPNPMRuntime(t *testing.T, url, hash string, libcKeys ...string) config.RuntimeConfig {
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
			ContentType: binmanager.BinContentTypeTarGz,
			BinaryPath:  new("pnpm"),
			ExtractDir:  true,
		}
	}
	return config.RuntimeConfig{
		Kind:    config.RuntimeKindPNPM,
		Mode:    config.RuntimeModeManaged,
		Managed: &config.RuntimeConfigManaged{Binaries: binmanager.MapOfBinaries{osType: {archType: libcMap}}},
		PNPM:    &config.RuntimeConfigPNPM{PNPMVersion: "12.4.1"},
	}
}

const pnpmStubContent = "#!/bin/sh\necho pnpm-stub\n"

// makePNPMArchive builds a tar.gz laid out like a pnpm 12 release archive: the
// binary at the root beside dist/, which carries the node-gyp pnpm builds
// native dependencies with. It returns the bytes and their SHA-256.
func makePNPMArchive(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	files := []struct {
		name string
		body string
		mode int64
	}{
		{"pnpm", pnpmStubContent, 0o755},
		{"dist/node_modules/node-gyp/package.json", `{"name":"node-gyp"}`, 0o644},
	}
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", f.name, err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatalf("write tar body %s: %v", f.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// TestPNPMRuntime_DownloadVerifyExtract pins that pnpm is acquired through the
// generic managed-runtime path: SHA-256-verified, extracted whole into its
// runtime store directory, and a cache hit afterwards.
func TestPNPMRuntime_DownloadVerifyExtract(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	body, hash := makePNPMArchive(t)
	var hits int32
	server := nodeArchiveServer(t, body, "/pnpm.tar.gz", &hits)
	defer server.Close()

	rm := New(config.MapOfRuntimes{testPNPMRuntimeName: hostPNPMRuntime(t, server.URL+"/pnpm.tar.gz", hash, testLibc)})
	want, err := rm.ResolveRuntimePath(testPNPMRuntimeName)
	if err != nil {
		t.Fatalf("ResolveRuntimePath() error = %v", err)
	}
	runtimeDir := filepath.Join(env.GetRuntimesPath(), testPNPMRuntimeName) + string(filepath.Separator)
	if !strings.HasPrefix(want, runtimeDir) || filepath.Base(want) != "pnpm" {
		t.Errorf("pnpm resolves to %q, want {store}/.runtimes/%s/<hash>/pnpm", want, testPNPMRuntimeName)
	}

	got, err := rm.getRuntimePath(context.Background(), testPNPMRuntimeName)
	if err != nil {
		t.Fatalf("getRuntimePath() error = %v", err)
	}
	if got != want {
		t.Errorf("getRuntimePath() = %q, want %q", got, want)
	}
	content, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read pnpm binary: %v", err)
	}
	if string(content) != pnpmStubContent {
		t.Errorf("pnpm binary content = %q, want %q", content, pnpmStubContent)
	}
	// pnpm finds node-gyp relative to its own binary, so the rest of the archive
	// has to survive next to it.
	nodeGyp := filepath.Join(filepath.Dir(got), "dist", "node_modules", "node-gyp", "package.json")
	if _, err := os.Stat(nodeGyp); err != nil {
		t.Errorf("node-gyp was not kept beside the pnpm binary: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("archive fetched %d times, want 1", n)
	}

	before := DownloaderConstructions()
	again, err := rm.getRuntimePath(context.Background(), testPNPMRuntimeName)
	if err != nil {
		t.Fatalf("second getRuntimePath() error = %v", err)
	}
	if again != got {
		t.Errorf("second getRuntimePath() = %q, want %q", again, got)
	}
	if DownloaderConstructions() != before {
		t.Error("an installed pnpm must be a cache hit, but a downloader was constructed")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("archive fetched %d times after a cache hit, want 1", n)
	}
}

func TestPNPMRuntime_SHA256Mismatch(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	body, _ := makePNPMArchive(t)
	var hits int32
	server := nodeArchiveServer(t, body, "/pnpm.tar.gz", &hits)
	defer server.Close()

	rm := New(config.MapOfRuntimes{
		testPNPMRuntimeName: hostPNPMRuntime(t, server.URL+"/pnpm.tar.gz", strings.Repeat("11", 32), testLibc),
	})
	_, err := rm.getRuntimePath(context.Background(), testPNPMRuntimeName)
	if err == nil {
		t.Fatal("expected an error for a SHA-256 mismatch, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "hash") {
		t.Errorf("error should report the hash mismatch, got: %v", err)
	}
	binPath, err := rm.ResolveRuntimePath(testPNPMRuntimeName)
	if err != nil {
		t.Fatalf("ResolveRuntimePath() error = %v", err)
	}
	if _, statErr := os.Stat(binPath); statErr == nil {
		t.Errorf("a pnpm build that failed verification was installed at %s", binPath)
	}
}

// TestGetAppPath_PNPMRuntimeIdentity pins where the pnpm runtime's identity
// lands: in the app hash of a Node app, never in the Node runtime's own store
// path, and never as a store path.
func TestGetAppPath_PNPMRuntimeIdentity(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	extra := PackageAppPathExtra{PackageName: "eslint", BinPath: "node_modules/.bin/eslint"}
	appPath := func(runtimes config.MapOfRuntimes) (string, error) {
		return New(runtimes).GetAppPath("eslint", config.RuntimeKindNode, "9.0.0", nil, "", nil, nil, "node", extra)
	}
	base := nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc)
	bumped := nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc)
	bumped[testPNPMRuntimeName] = pnpmRuntimeWithVersion("12.5.0")

	t.Run("a pnpm bump moves the app but not the node runtime", func(t *testing.T) {
		basePath, err := appPath(base)
		if err != nil {
			t.Fatalf("GetAppPath() error = %v", err)
		}
		bumpedPath, err := appPath(bumped)
		if err != nil {
			t.Fatalf("GetAppPath() error = %v", err)
		}
		if basePath == bumpedPath {
			t.Error("a different pnpm runtime must produce a different app path")
		}

		baseNode, err := New(base).ResolveRuntimePath("node")
		if err != nil {
			t.Fatalf("ResolveRuntimePath() error = %v", err)
		}
		bumpedNode, err := New(bumped).ResolveRuntimePath("node")
		if err != nil {
			t.Fatalf("ResolveRuntimePath() error = %v", err)
		}
		if baseNode != bumpedNode {
			t.Errorf("a pnpm bump moved the node runtime (%q -> %q), which would re-download Node", baseNode, bumpedNode)
		}
	})

	t.Run("pnpmRuntime selects the pnpm runtime by name", func(t *testing.T) {
		runtimes := nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc)
		runtimes["pnpm-next"] = pnpmRuntimeWithVersion("12.5.0")
		node := runtimes["node"]
		node.Node = &config.RuntimeConfigNode{NodeVersion: node.Node.NodeVersion, PNPMRuntime: "pnpm-next"}
		runtimes["node"] = node

		got, err := appPath(runtimes)
		if err != nil {
			t.Fatalf("GetAppPath() error = %v", err)
		}
		// The runtime's name is not part of its identity: the same pnpm under a
		// different name is the same install.
		want, err := appPath(bumped)
		if err != nil {
			t.Fatalf("GetAppPath() error = %v", err)
		}
		if got != want {
			t.Errorf("GetAppPath() = %q, want the path of the referenced pnpm runtime %q", got, want)
		}
	})

	t.Run("a missing pnpm runtime is an error", func(t *testing.T) {
		runtimes := nodeRuntimeWith(t, "https://example.com/node.tar.xz", "abc", testLibc)
		delete(runtimes, testPNPMRuntimeName)
		if _, err := appPath(runtimes); err == nil || !strings.Contains(err.Error(), "pnpm runtime") {
			t.Fatalf("GetAppPath() error = %v, want a pnpm runtime resolution error", err)
		}
	})

	t.Run("the pnpm identity is path-free", func(t *testing.T) {
		under := func(root string) string {
			t.Setenv("DATAMITSU_CACHE_DIR", root)
			got, err := appPath(base)
			if err != nil {
				t.Fatalf("GetAppPath() error = %v", err)
			}
			return got
		}
		first, second := under(t.TempDir()), under(t.TempDir())
		if filepath.Base(first) != filepath.Base(second) {
			t.Errorf("app hash depends on the store root: %q vs %q", first, second)
		}
	})
}

// TestCollectRequiredRuntimes_PNPM pins that a needed Node or Bun runtime pulls
// in the pnpm runtime that installs its apps, and that nothing else does.
func TestCollectRequiredRuntimes_PNPM(t *testing.T) {
	nodeRef := func(ref string) config.RuntimeConfig {
		return config.RuntimeConfig{
			Kind: config.RuntimeKindNode,
			Mode: config.RuntimeModeManaged,
			Node: &config.RuntimeConfigNode{NodeVersion: "26.2.0", PNPMRuntime: ref},
		}
	}
	runtimes := config.MapOfRuntimes{
		"node-explicit": nodeRef("pnpm-b"),
		"node-default":  nodeRef(""),
		"node-dangling": nodeRef("ghost"),
		"bun-explicit": {
			Kind: config.RuntimeKindBun,
			Mode: config.RuntimeModeManaged,
			Bun:  &config.RuntimeConfigBun{BunVersion: "1.4.1", PNPMRuntime: "pnpm-b"},
		},
		"pnpm-a": pnpmRuntimeWithVersion("12.4.1"),
		"pnpm-b": pnpmRuntimeWithVersion("12.5.0"),
		"uv":     {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeManaged},
		"jvm":    {Kind: config.RuntimeKindJVM, Mode: config.RuntimeModeManaged, JVM: &config.RuntimeConfigJVM{JavaVersion: "21"}},
		"go":     {Kind: config.RuntimeKindGo, Mode: config.RuntimeModeManaged, Go: &config.RuntimeConfigGo{GoVersion: "1.22.0"}},
	}
	nodeApp := func(runtimeRef string) binmanager.App {
		return binmanager.App{Required: true, Node: &binmanager.AppConfigNode{PackageName: "x", Version: "1", BinPath: "b", Runtime: runtimeRef}}
	}

	tests := []struct {
		name string
		apps binmanager.MapOfApps
		want []string
	}{
		{"explicit pnpmRuntime", binmanager.MapOfApps{"a": nodeApp("node-explicit")}, []string{"node-explicit", "pnpm-b"}},
		{
			"bun follows its pnpmRuntime",
			binmanager.MapOfApps{"a": {Required: true, Bun: &binmanager.AppConfigBun{PackageName: "x", Version: "1", BinPath: "b", Runtime: "bun-explicit"}}},
			[]string{"bun-explicit", "pnpm-b"},
		},
		{"empty pnpmRuntime falls back to the first pnpm runtime by name", binmanager.MapOfApps{"a": nodeApp("node-default")}, []string{"node-default", "pnpm-a"}},
		{"dangling pnpmRuntime contributes nothing", binmanager.MapOfApps{"a": nodeApp("node-dangling")}, []string{"node-dangling"}},
		{
			"uv, jvm and go apps need no pnpm",
			binmanager.MapOfApps{
				"u": {Required: true, Uv: &binmanager.AppConfigUV{PackageName: "x", Version: "1", Runtime: "uv"}},
				"j": {Required: true, Jvm: &binmanager.AppConfigJVM{JarURL: "https://x/x.jar", JarHash: "h", Version: "1", Runtime: "jvm"}},
				"g": {Required: true, Go: &binmanager.AppConfigGo{PackageName: "x", Version: "1", Runtime: "go"}},
			},
			[]string{"go", "jvm", "uv"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CollectRequiredRuntimes(tt.apps, runtimes, false); !slices.Equal(got, tt.want) {
				t.Errorf("CollectRequiredRuntimes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildPNPMInstallArgs(t *testing.T) {
	tests := []struct {
		name    string
		hasLock bool
		want    []string
	}{
		{"without lockfile", false, []string{"install", "--reporter=ndjson"}},
		{"with lockfile includes --frozen-lockfile", true, []string{"install", "--reporter=ndjson", "--frozen-lockfile"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildPNPMInstallArgs(tt.hasLock); !slices.Equal(got, tt.want) {
				t.Errorf("buildPNPMInstallArgs(%v) = %v, want %v", tt.hasLock, got, tt.want)
			}
		})
	}
}

// TestBuildPackageJSON pins the package.json that drives `pnpm install`: the
// project name sanitizer (scoped npm names must become a valid npm `name`), the
// package self-dependency, the merged extra dependencies, and the fixed
// private/type/version fields.
func TestBuildPackageJSON(t *testing.T) {
	parse := func(t *testing.T, raw []byte) map[string]any {
		t.Helper()
		var pkg map[string]any
		if err := json.Unmarshal(raw, &pkg); err != nil {
			t.Fatalf("buildPackageJSON produced invalid JSON: %v\n%s", err, raw)
		}
		return pkg
	}

	t.Run("scoped package name is sanitized into a valid npm name", func(t *testing.T) {
		raw, err := buildPackageJSON("@scope/pkg", "1.2.3", nil)
		if err != nil {
			t.Fatalf("buildPackageJSON() error = %v", err)
		}
		pkg := parse(t, raw)
		// "@" stripped, "/" → "-": @scope/pkg -> scope-pkg.
		if got := pkg["name"]; got != "datamitsu-app-scope-pkg" {
			t.Errorf("name = %v, want %q", got, "datamitsu-app-scope-pkg")
		}
		deps, ok := pkg["dependencies"].(map[string]any)
		if !ok {
			t.Fatalf("dependencies missing or wrong type: %T", pkg["dependencies"])
		}
		if got := deps["@scope/pkg"]; got != "1.2.3" {
			t.Errorf("self-dependency @scope/pkg = %v, want %q", got, "1.2.3")
		}
		if pkg["private"] != true {
			t.Errorf("private = %v, want true", pkg["private"])
		}
		if pkg["type"] != "module" {
			t.Errorf("type = %v, want %q", pkg["type"], "module")
		}
		if pkg["version"] != "0.0.0" {
			t.Errorf("version = %v, want %q", pkg["version"], "0.0.0")
		}
	})

	t.Run("extra dependencies are merged alongside the package itself", func(t *testing.T) {
		raw, err := buildPackageJSON("eslint", "9.0.0", map[string]string{
			"eslint-plugin-import": "2.29.0",
		})
		if err != nil {
			t.Fatalf("buildPackageJSON() error = %v", err)
		}
		pkg := parse(t, raw)
		if got := pkg["name"]; got != "datamitsu-app-eslint" {
			t.Errorf("name = %v, want %q", got, "datamitsu-app-eslint")
		}
		deps, ok := pkg["dependencies"].(map[string]any)
		if !ok {
			t.Fatalf("dependencies missing or wrong type: %T", pkg["dependencies"])
		}
		if got := deps["eslint"]; got != "9.0.0" {
			t.Errorf("eslint dep = %v, want %q", got, "9.0.0")
		}
		if got := deps["eslint-plugin-import"]; got != "2.29.0" {
			t.Errorf("eslint-plugin-import dep = %v, want %q", got, "2.29.0")
		}
	})
}

// TestFilesWithMergedWorkspaceYAML_HashIncludesDefaults pins the invariant
// that the node app cache key incorporates the merged pnpm-workspace.yaml
// content (defaults + user override), not just the user override. Without
// this, tightening pnpmdefaults.Defaults in a future release would not
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
	cfg := pnpmdefaults.Defaults()

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
		defaults := pnpmdefaults.Defaults()
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
		defaults := pnpmdefaults.Defaults()
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
		defaults := pnpmdefaults.Defaults()
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
		defaults := pnpmdefaults.Defaults()
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

	t.Run("storeDir pins the pnpm store inside the datamitsu store", func(t *testing.T) {
		// pnpm 11 ignores npm_config_store_dir; the workspace storeDir key is the
		// only mechanism that keeps the pnpm store under GetStorePath() so that
		// `datamitsu store clear` removes it. datamitsu owns this path, so a user
		// pnpm-workspace.yaml must not be able to relocate the store elsewhere.
		t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
		files := map[string]string{
			"pnpm-workspace.yaml": "storeDir: /tmp/attacker-controlled-store\n",
		}
		yamlOut, err := buildPNPMWorkspaceForApp(files)
		if err != nil {
			t.Fatalf("buildPNPMWorkspaceForApp() error = %v", err)
		}

		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlOut), &parsed); err != nil {
			t.Fatalf("failed to parse output YAML: %v", err)
		}
		if !pnpmWorkspaceValueEqual(parsed["storeDir"], env.GetPNPMStorePath()) {
			t.Errorf("storeDir = %v, want %q (user override must not win)", parsed["storeDir"], env.GetPNPMStorePath())
		}
	})
}

func prepareAndWriteWorkspace(t *testing.T, appEnvPath string, files map[string]string) map[string]string {
	t.Helper()
	mergedYAML, err := buildPNPMWorkspaceForApp(files)
	if err != nil {
		t.Fatalf("buildPNPMWorkspaceForApp() error = %v", err)
	}
	filtered := filesWithoutWorkspaceYAML(files)
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

		if _, err := buildPNPMWorkspaceForApp(files); err == nil {
			t.Error("expected error for invalid user YAML, got nil")
		}
	})

	t.Run("post-write overwrites pre-existing file (archive ordering invariant)", func(t *testing.T) {
		appEnvPath := t.TempDir()

		archiveContent := "strictDepBuilds: false\nallowBuilds:\n  malicious: true\n"
		if err := os.WriteFile(filepath.Join(appEnvPath, "pnpm-workspace.yaml"), []byte(archiveContent), 0o644); err != nil {
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

	// Mirrors the node install ordering after the redundant os.MkdirAll was
	// removed from installNodeAppOnce: writeAppWorkspaceFile must create a
	// missing app dir so a subsequent package.json write into the same dir
	// succeeds without any prior MkdirAll.
	t.Run("creates missing app dir so a following package.json write succeeds", func(t *testing.T) {
		appEnvPath := filepath.Join(t.TempDir(), "nested", "app")

		if err := writeAppWorkspaceFile(appEnvPath, "key: value\n"); err != nil {
			t.Fatalf("writeAppWorkspaceFile() error = %v", err)
		}
		if _, err := os.Stat(appEnvPath); err != nil {
			t.Fatalf("app dir not created: %v", err)
		}
		pkgPath := filepath.Join(appEnvPath, "package.json")
		if err := os.WriteFile(pkgPath, []byte("{}"), 0o644); err != nil {
			t.Fatalf("writing package.json after writeAppWorkspaceFile failed: %v", err)
		}
	})

	// MkdirAll fails when a path component is an existing regular file: seed a
	// plain file at the app dir's parent so os.MkdirAll cannot create the app
	// dir, exercising the "failed to create app directory" wrap.
	t.Run("MkdirAll failure returns wrapped create-directory error", func(t *testing.T) {
		base := t.TempDir()
		parentFile := filepath.Join(base, "not-a-dir")
		if err := os.WriteFile(parentFile, []byte("regular file"), 0o644); err != nil {
			t.Fatalf("failed to seed regular file: %v", err)
		}
		// appEnvPath's parent (parentFile) is a regular file, so MkdirAll errors.
		appEnvPath := filepath.Join(parentFile, "app-env")

		err := writeAppWorkspaceFile(appEnvPath, "key: value\n")
		if err == nil {
			t.Fatal("expected error when app dir's parent is a regular file, got nil")
		}
		if !strings.Contains(err.Error(), "failed to create app directory") {
			t.Errorf("error should mention failed to create app directory, got: %v", err)
		}
	})
}

// TestFilesWithMergedWorkspaceYAML_InvalidUserYAML pins the error path of
// filesWithMergedWorkspaceYAML: an invalid user pnpm-workspace.yaml must
// propagate the merge error (from buildPNPMWorkspaceForApp) rather than
// returning a partially-built files map.
func TestFilesWithMergedWorkspaceYAML_InvalidUserYAML(t *testing.T) {
	files := map[string]string{
		"pnpm-workspace.yaml": "not: valid: yaml: at: all: [",
	}
	out, err := filesWithMergedWorkspaceYAML(files)
	if err == nil {
		t.Fatal("expected error for invalid user pnpm-workspace.yaml, got nil")
	}
	if out != nil {
		t.Errorf("expected nil files map on error, got %v", out)
	}
}
