package runtimemanager

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// These tests drive a real uv against a loopback PEP 691 index, so they prove
// what the lock records and what `uv sync --locked` accepts rather than which
// flags datamitsu builds. They skip when uv or a Python >= 3.12 is missing.

type testWheel struct {
	name, version string
	requires      []string
	uploaded      time.Time
	data          []byte
}

func (w *testWheel) filename() string {
	return fmt.Sprintf("%s-%s-py3-none-any.whl", w.name, w.version)
}

func buildTestWheel(t *testing.T, w *testWheel) {
	t.Helper()
	distInfo := fmt.Sprintf("%s-%s.dist-info", w.name, w.version)
	var metadata strings.Builder
	fmt.Fprintf(&metadata, "Metadata-Version: 2.1\nName: %s\nVersion: %s\n", w.name, w.version)
	for _, r := range w.requires {
		fmt.Fprintf(&metadata, "Requires-Dist: %s\n", r)
	}
	files := [][2]string{
		{w.name + "/__init__.py", "def main():\n    print('ok')\n"},
		{distInfo + "/METADATA", metadata.String()},
		// cspell:ignore Purelib
		{distInfo + "/WHEEL", "Wheel-Version: 1.0\nGenerator: datamitsu-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{distInfo + "/entry_points.txt", fmt.Sprintf("[console_scripts]\n%s = %s:main\n", w.name, w.name)},
	}
	var record strings.Builder
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		fw, err := zw.Create(f[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(f[1]))
		fmt.Fprintf(&record, "%s,sha256=%s,%d\n", f[0], base64.RawURLEncoding.EncodeToString(sum[:]), len(f[1]))
	}
	record.WriteString(distInfo + "/RECORD,,\n")
	fw, err := zw.Create(distInfo + "/RECORD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(record.String())); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	w.data = buf.Bytes()
}

func serveTestIndex(t *testing.T, wheels []*testWheel) string {
	t.Helper()
	for _, w := range wheels {
		buildTestWheel(t, w)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if name, ok := strings.CutPrefix(r.URL.Path, "/files/"); ok {
			for _, w := range wheels {
				if w.filename() == name {
					http.ServeContent(rw, r, name, w.uploaded, bytes.NewReader(w.data))
					return
				}
			}
		}
		project := strings.Trim(strings.TrimPrefix(r.URL.Path, "/simple/"), "/")
		type file struct {
			Filename   string            `json:"filename"`
			URL        string            `json:"url"`
			Hashes     map[string]string `json:"hashes"`
			UploadTime string            `json:"upload-time"`
			Size       int               `json:"size"`
		}
		page := struct {
			Meta     map[string]string `json:"meta"`
			Name     string            `json:"name"`
			Versions []string          `json:"versions"`
			Files    []file            `json:"files"`
		}{Meta: map[string]string{"api-version": "1.1"}, Name: project}
		for _, w := range wheels {
			if w.name != project {
				continue
			}
			sum := sha256.Sum256(w.data)
			page.Versions = append(page.Versions, w.version)
			page.Files = append(page.Files, file{
				Filename:   w.filename(),
				URL:        srv.URL + "/files/" + w.filename(),
				Hashes:     map[string]string{"sha256": hex.EncodeToString(sum[:])},
				UploadTime: w.uploaded.UTC().Format(time.RFC3339),
				Size:       len(w.data),
			})
		}
		if len(page.Files) == 0 {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", "application/vnd.pypi.simple.v1+json")
		_ = json.NewEncoder(rw).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/simple"
}

func requireUVWithPython(t *testing.T) string {
	t.Helper()
	uvBin, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv is not on PATH; what a uv lock records for the release-age window is left unverified")
	}
	cmd := exec.Command(uvBin, "python", "find", "--no-config", ">=3.12")
	cmd.Env = append(os.Environ(), "UV_PYTHON_DOWNLOADS=never", "UV_PYTHON_INSTALL_DIR="+t.TempDir())
	if err := cmd.Run(); err != nil {
		t.Skip("uv finds no Python >= 3.12 without downloading one; the release-age window is left unverified")
	}
	return uvBin
}

type uvReleaseAgeFixture struct {
	t        *testing.T
	uvBin    string
	indexEnv map[string]string
}

func newUVReleaseAgeFixture(t *testing.T) *uvReleaseAgeFixture {
	t.Helper()
	uvBin := requireUVWithPython(t)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Now().Add(-time.Hour)
	index := serveTestIndex(t, []*testWheel{
		{name: "widget", version: "1.0.0", requires: []string{"gadget"}, uploaded: old},
		{name: "widget", version: "3.0.0", requires: []string{"gadget"}, uploaded: fresh},
		{name: "gadget", version: "1.0.0", uploaded: old},
		{name: "gadget", version: "2.0.0", uploaded: fresh},
	})
	return &uvReleaseAgeFixture{
		t:        t,
		uvBin:    uvBin,
		indexEnv: map[string]string{"UV_DEFAULT_INDEX": index, "UV_PYTHON_DOWNLOADS": "never"},
	}
}

// install runs one install in a store of its own, so every call starts clean,
// and returns the uv.lock left in the app directory.
func (f *uvReleaseAgeFixture) install(version, lockFile string, files map[string]string) (string, error) {
	f.t.Helper()
	f.t.Setenv("DATAMITSU_CACHE_DIR", f.t.TempDir())
	rm := New(config.MapOfRuntimes{
		"uv": {Kind: config.RuntimeKindUV, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: f.uvBin}},
	})
	app := &binmanager.AppConfigUV{PackageName: "widget", Version: version, Runtime: "uv", LockFile: lockFile}
	if err := rm.InstallUVApp(context.Background(), "widget", app, f.indexEnv, files, nil); err != nil {
		return "", err
	}
	appEnvPath, err := rm.GetAppPath("widget", config.RuntimeKindUV, uvVersionForHash(version, ""), nil, lockFileHash(lockFile), files, nil, "uv")
	if err != nil {
		f.t.Fatal(err)
	}
	lock, err := os.ReadFile(filepath.Join(appEnvPath, "uv.lock"))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(lock), nil
}

func (f *uvReleaseAgeFixture) compress(lock string) string {
	f.t.Helper()
	compressed, err := CompressLockFile(lock)
	if err != nil {
		f.t.Fatal(err)
	}
	return compressed
}

func lockedVersion(lock, name string) string {
	m := regexp.MustCompile(`(?m)^name = "` + regexp.QuoteMeta(name) + `"\nversion = "([^"]+)"`).FindStringSubmatch(lock)
	if m == nil {
		return ""
	}
	return m[1]
}

// withAmbientUVSettings exports the settings a developer might keep for their
// own uv work. None of them may reach a managed install.
func withAmbientUVSettings(t *testing.T) {
	t.Helper()
	userConfig := filepath.Join(t.TempDir(), "uv.toml")
	if err := os.WriteFile(userConfig, []byte("exclude-newer = \"8 days\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UV_CONFIG_FILE", userConfig)
	t.Setenv("UV_EXCLUDE_NEWER", "8 days")
	t.Setenv("UV_EXCLUDE_NEWER_PACKAGE", "gadget=P1D")
}

func TestUVLockRecordsTheReleaseAgeWindow(t *testing.T) {
	f := newUVReleaseAgeFixture(t)

	lock, err := f.install("1.0.0", "", nil)
	if err != nil {
		t.Fatalf("resolving without a lock file: %v", err)
	}
	if !strings.Contains(lock, `exclude-newer-span = "P7D"`) {
		t.Fatalf("lock does not record the P7D window:\n%s", lock)
	}
	if got := lockedVersion(lock, "gadget"); got != "1.0.0" {
		t.Errorf("transitive gadget resolved to %q, want 1.0.0 (2.0.0 is younger than the window)", got)
	}

	withAmbientUVSettings(t)
	if _, err := f.install("1.0.0", f.compress(lock), nil); err != nil {
		t.Errorf("a lock with a window must install with --locked in a clean store: %v", err)
	}
}

func TestUVLockWithoutOptionsInstallsDespiteAmbientSettings(t *testing.T) {
	f := newUVReleaseAgeFixture(t)

	lock, err := f.install("1.0.0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A lock generated before datamitsu passed a window has no [options].
	legacy := regexp.MustCompile(`(?s)\[options\]\n.*?\n\n`).ReplaceAllString(lock, "")
	if strings.Contains(legacy, "exclude-newer") {
		t.Fatalf("failed to strip [options]:\n%s", legacy)
	}

	withAmbientUVSettings(t)
	if _, err := f.install("1.0.0", f.compress(legacy), nil); err != nil {
		t.Errorf("a lock without [options] must keep installing: %v", err)
	}
}

func TestUVAppConfigExemptsAPackageFromTheWindow(t *testing.T) {
	f := newUVReleaseAgeFixture(t)
	files := map[string]string{"uv.toml": "[exclude-newer-package]\ngadget = false\n"}

	lock, err := f.install("1.0.0", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if got := lockedVersion(lock, "gadget"); got != "2.0.0" {
		t.Errorf("exempt gadget resolved to %q, want 2.0.0", got)
	}
	if !strings.Contains(lock, "[options.exclude-newer-package]") {
		t.Fatalf("lock does not record the exemption:\n%s", lock)
	}

	withAmbientUVSettings(t)
	if _, err := f.install("1.0.0", f.compress(lock), files); err != nil {
		t.Errorf("a lock with a package exemption must install with --locked in a clean store: %v", err)
	}
}

func TestUVTopLevelYoungerThanTheWindowNamesTheReleaseAge(t *testing.T) {
	f := newUVReleaseAgeFixture(t)

	_, err := f.install("3.0.0", "", nil)
	if err == nil {
		t.Fatal("expected a top-level version younger than the window to fail to resolve")
	}
	if !strings.Contains(err.Error(), "runtimeconfig.MinimumReleaseAgeMinutes") {
		t.Errorf("error does not explain the release-age window: %v", err)
	}
}
