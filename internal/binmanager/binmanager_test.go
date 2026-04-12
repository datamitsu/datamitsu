package binmanager

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
)

type mockRuntimeAppManager struct {
	getCommandInfoFunc func(appName string, app App) (*CommandInfo, error)
	computeAppPathFunc func(appName string, app App) (string, error)
}

func (m *mockRuntimeAppManager) GetCommandInfo(appName string, app App) (*CommandInfo, error) {
	if m.getCommandInfoFunc != nil {
		return m.getCommandInfoFunc(appName, app)
	}
	return nil, fmt.Errorf("mock: not implemented")
}

func (m *mockRuntimeAppManager) ComputeAppPath(appName string, app App) (string, error) {
	if m.computeAppPathFunc != nil {
		return m.computeAppPathFunc(appName, app)
	}
	return "", fmt.Errorf("mock: ComputeAppPath not implemented")
}

// TestConcurrentDownloadSameBinary verifies concurrent downloads of the same
// binary do not cause Go data races. The expected behavior is no race detected.
// Duplicate downloads may occur (last-write-wins via atomic os.Rename), but
// the final binary is always valid since all goroutines download the same
// verified content.
func TestConcurrentDownloadSameBinary(t *testing.T) {
	testContent := []byte("#!/bin/sh\necho hello\n")
	hash := sha256.Sum256(testContent)
	expectedHash := hex.EncodeToString(hash[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("failed to get OS type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("failed to get arch type: %v", err)
	}

	bm := New(MapOfApps{
		"testbin": App{
			Required: true,
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					osType: {
						archType: {"unknown": BinaryOsArchInfo{
							URL:         server.URL,
							Hash:        expectedHash,
							ContentType: BinContentTypeBinary,
						}},
					},
				},
			},
		},
	}, nil, nil)

	const goroutines = 5
	errs := make([]error, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = bm.downloadInternal("testbin", nil)
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: downloadInternal() error = %v", i, err)
		}
	}

	binPath, err := bm.getBinaryPath("testbin")
	if err != nil {
		t.Fatalf("getBinaryPath() error = %v", err)
	}

	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Error("binary not found at expected path after concurrent downloads")
	}
}

func TestMergeExecEnv_CINotForced(t *testing.T) {
	result := mergeExecEnv([]string{"PATH=/usr/bin"}, nil)
	for _, e := range result {
		if e == "CI=true" {
			t.Errorf("CI=true should not be forced, got %v", result)
		}
	}
}

func TestMergeExecEnv_CIPreservedFromBase(t *testing.T) {
	result := mergeExecEnv([]string{"CI=false", "PATH=/usr/bin"}, nil)
	if !slices.Contains(result, "CI=false") {
		t.Errorf("expected CI=false to be preserved from base, got %v", result)
	}
}

func TestMergeExecEnv_AppEnvCanOverrideCI(t *testing.T) {
	result := mergeExecEnv([]string{"CI=true", "PATH=/usr/bin"}, map[string]string{"CI": "false"})
	if !slices.Contains(result, "CI=false") {
		t.Errorf("expected appEnv CI=false to override base CI=true, got %v", result)
	}
}

func TestMergeExecEnv_AppEnvOverwrite(t *testing.T) {
	base := []string{"FOO=old", "PATH=/usr/bin"}
	appEnv := map[string]string{"FOO": "new"}
	result := mergeExecEnv(base, appEnv)
	if !slices.Contains(result, "FOO=new") {
		t.Errorf("expected FOO=new in result, got %v", result)
	}
	for _, e := range result {
		if e == "FOO=old" {
			t.Errorf("unexpected FOO=old still present in result: %v", result)
		}
	}
}

func TestMergeExecEnv_AppEnvNewKey(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	appEnv := map[string]string{"MY_VAR": "hello"}
	result := mergeExecEnv(base, appEnv)
	if !slices.Contains(result, "MY_VAR=hello") {
		t.Errorf("expected MY_VAR=hello in result, got %v", result)
	}
}

func TestMergeExecEnv_BaseUnchanged(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}
	original := make([]string, len(base))
	copy(original, base)
	mergeExecEnv(base, map[string]string{"NEW": "val"})
	for i, v := range base {
		if v != original[i] {
			t.Errorf("base slice was mutated at index %d: got %q, want %q", i, v, original[i])
		}
	}
}

func TestGetCommandInfo_ShellApp(t *testing.T) {
	bm := New(MapOfApps{
		"myshell": App{
			Shell: &AppConfigShell{
				Name: "bash",
				Args: []string{"-c", "echo hello"},
				Env:  map[string]string{"FOO": "bar"},
			},
		},
	}, nil, nil)

	info, err := bm.GetCommandInfo("myshell")
	if err != nil {
		t.Fatalf("GetCommandInfo() error = %v", err)
	}
	if info.Type != "shell" {
		t.Errorf("expected type 'shell', got %q", info.Type)
	}
	if info.Command != "bash" {
		t.Errorf("expected command 'bash', got %q", info.Command)
	}
	if len(info.Args) != 2 || info.Args[0] != "-c" {
		t.Errorf("expected args [-c echo hello], got %v", info.Args)
	}
	if info.Env["FOO"] != "bar" {
		t.Errorf("expected env FOO=bar, got %v", info.Env)
	}
}

func TestGetCommandInfo_UVApp_DelegatesToRuntimeManager(t *testing.T) {
	expectedInfo := &CommandInfo{
		Type:    "uv",
		Command: "/cache/apps/uv/yamllint/abc123/bin/yamllint",
		Env: map[string]string{
			"UV_TOOL_DIR": "/cache/apps/uv/yamllint/abc123/tools",
		},
	}

	mock := &mockRuntimeAppManager{
		getCommandInfoFunc: func(appName string, app App) (*CommandInfo, error) {
			if appName != "yamllint" {
				t.Errorf("expected appName 'yamllint', got %q", appName)
			}
			if app.Uv == nil {
				t.Error("expected app.Uv to be non-nil")
			}
			if app.Uv.PackageName != "yamllint" {
				t.Errorf("expected packageName 'yamllint', got %q", app.Uv.PackageName)
			}
			return expectedInfo, nil
		},
	}

	bm := New(MapOfApps{
		"yamllint": App{
			Uv: &AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
	}, nil, mock)

	info, err := bm.GetCommandInfo("yamllint")
	if err != nil {
		t.Fatalf("GetCommandInfo() error = %v", err)
	}
	if info != expectedInfo {
		t.Errorf("expected info to be delegated result, got %+v", info)
	}
}

func TestGetCommandInfo_UVApp_NoRuntimeManager(t *testing.T) {
	bm := New(MapOfApps{
		"yamllint": App{
			Uv: &AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
	}, nil, nil)

	_, err := bm.GetCommandInfo("yamllint")
	if err == nil {
		t.Fatal("expected error when no runtime manager configured")
	}
}

func TestAppConfigFNM_Fields(t *testing.T) {
	cfg := AppConfigFNM{
		PackageName:  "@mermaid-js/mermaid-cli",
		Version:      "11.12.0",
		BinPath:      "node_modules/.bin/mmdc",
		Runtime:      "fnm",
		LockFile:     "lockfile: content",
		Dependencies: map[string]string{"playwright": "1.52.0"},
	}

	if cfg.PackageName != "@mermaid-js/mermaid-cli" {
		t.Errorf("PackageName = %q, want %q", cfg.PackageName, "@mermaid-js/mermaid-cli")
	}
	if cfg.Version != "11.12.0" {
		t.Errorf("Version = %q, want %q", cfg.Version, "11.12.0")
	}
	if cfg.BinPath != "node_modules/.bin/mmdc" {
		t.Errorf("BinPath = %q, want %q", cfg.BinPath, "node_modules/.bin/mmdc")
	}
	if cfg.Runtime != "fnm" {
		t.Errorf("Runtime = %q, want %q", cfg.Runtime, "fnm")
	}
	if cfg.LockFile != "lockfile: content" {
		t.Errorf("LockFile = %q, want %q", cfg.LockFile, "lockfile: content")
	}
	if cfg.Dependencies["playwright"] != "1.52.0" {
		t.Errorf("Dependencies[playwright] = %q, want %q", cfg.Dependencies["playwright"], "1.52.0")
	}
}

func TestAppConfigFNM_OptionalFields(t *testing.T) {
	cfg := AppConfigFNM{
		PackageName: "eslint",
		Version:     "9.0.0",
		BinPath:     "node_modules/.bin/eslint",
	}

	if cfg.Runtime != "" {
		t.Errorf("Runtime should be empty, got %q", cfg.Runtime)
	}
	if cfg.LockFile != "" {
		t.Errorf("LockFile should be empty, got %q", cfg.LockFile)
	}
	if cfg.Dependencies != nil {
		t.Errorf("Dependencies should be nil, got %v", cfg.Dependencies)
	}
}

func TestApp_FnmField(t *testing.T) {
	app := App{
		Required: true,
		Fnm: &AppConfigFNM{
			PackageName: "@mermaid-js/mermaid-cli",
			Version:     "11.12.0",
			BinPath:     "node_modules/.bin/mmdc",
		},
	}

	if app.Fnm == nil {
		t.Fatal("expected Fnm to be non-nil")
	}
	if app.Binary != nil {
		t.Error("expected Binary to be nil")
	}
	if app.Uv != nil {
		t.Error("expected Uv to be nil")
	}
	if app.Shell != nil {
		t.Error("expected Shell to be nil")
	}
}

func TestGetCommandInfo_FNMApp_DelegatesToRuntimeManager(t *testing.T) {
	expectedInfo := &CommandInfo{
		Type:    "fnm",
		Command: "/cache/runtimes/fnm-nodes/v22.14.0/installation/bin/node",
		Args:    []string{"/cache/apps/fnm/mmdc/xyz789/node_modules/.bin/mmdc"},
		Env: map[string]string{
			"npm_config_store_dir": "/cache/apps/fnm/mmdc/xyz789/.pnpm-store",
		},
	}

	mock := &mockRuntimeAppManager{
		getCommandInfoFunc: func(appName string, app App) (*CommandInfo, error) {
			if appName != "mmdc" {
				t.Errorf("expected appName 'mmdc', got %q", appName)
			}
			if app.Fnm == nil {
				t.Error("expected app.Fnm to be non-nil")
			}
			if app.Fnm.PackageName != "@mermaid-js/mermaid-cli" {
				t.Errorf("expected packageName '@mermaid-js/mermaid-cli', got %q", app.Fnm.PackageName)
			}
			return expectedInfo, nil
		},
	}

	bm := New(MapOfApps{
		"mmdc": App{
			Fnm: &AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.12.0",

				BinPath:     "node_modules/.bin/mmdc",
			},
		},
	}, nil, mock)

	info, err := bm.GetCommandInfo("mmdc")
	if err != nil {
		t.Fatalf("GetCommandInfo() error = %v", err)
	}
	if info != expectedInfo {
		t.Errorf("expected info to be delegated result, got %+v", info)
	}
}

func TestGetCommandInfo_FNMApp_NoRuntimeManager(t *testing.T) {
	bm := New(MapOfApps{
		"mmdc": App{
			Fnm: &AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.12.0",

				BinPath:     "node_modules/.bin/mmdc",
			},
		},
	}, nil, nil)

	_, err := bm.GetCommandInfo("mmdc")
	if err == nil {
		t.Fatal("expected error when no runtime manager configured")
	}
}

func TestGetAppsList_FNMApp(t *testing.T) {
	bm := New(MapOfApps{
		"mmdc": App{
			Fnm: &AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.12.0",

				BinPath:     "node_modules/.bin/mmdc",
			},
		},
	}, nil, nil)

	apps := bm.GetAppsList()
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Type != "fnm" {
		t.Errorf("expected type 'fnm', got %q", apps[0].Type)
	}
	if apps[0].Name != "mmdc" {
		t.Errorf("expected name 'mmdc', got %q", apps[0].Name)
	}
	if apps[0].Version != "11.12.0" {
		t.Errorf("expected version '11.12.0', got %q", apps[0].Version)
	}
	if apps[0].PackageName != "@mermaid-js/mermaid-cli" {
		t.Errorf("expected packageName '@mermaid-js/mermaid-cli', got %q", apps[0].PackageName)
	}
}

func TestGetAppsList_BinaryApp(t *testing.T) {
	bm := New(MapOfApps{
		"golangci-lint": App{
			Description: "Go linter",
			Binary: &AppConfigBinary{
				Version: "v2.7.2",
			},
		},
	}, nil, nil)

	apps := bm.GetAppsList()
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Type != "binary" {
		t.Errorf("expected type 'binary', got %q", apps[0].Type)
	}
	if apps[0].Version != "v2.7.2" {
		t.Errorf("expected version 'v2.7.2', got %q", apps[0].Version)
	}
	if apps[0].Description != "Go linter" {
		t.Errorf("expected description 'Go linter', got %q", apps[0].Description)
	}
}

func TestGetAppsList_UVApp(t *testing.T) {
	bm := New(MapOfApps{
		"yamllint": App{
			Uv: &AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
	}, nil, nil)

	apps := bm.GetAppsList()
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Type != "uv" {
		t.Errorf("expected type 'uv', got %q", apps[0].Type)
	}
	if apps[0].Version != "1.38.0" {
		t.Errorf("expected version '1.38.0', got %q", apps[0].Version)
	}
	if apps[0].PackageName != "yamllint" {
		t.Errorf("expected packageName 'yamllint', got %q", apps[0].PackageName)
	}
}

func TestGetAppsList_ShellApp(t *testing.T) {
	bm := New(MapOfApps{
		"echo": App{
			Shell: &AppConfigShell{
				Name: "echo",
			},
		},
	}, nil, nil)

	apps := bm.GetAppsList()
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Type != "shell" {
		t.Errorf("expected type 'shell', got %q", apps[0].Type)
	}
	if apps[0].Command != "echo" {
		t.Errorf("expected command 'echo', got %q", apps[0].Command)
	}
	if apps[0].Version != "" {
		t.Errorf("expected empty version for shell app, got %q", apps[0].Version)
	}
}

func TestGetAppsList_AllTypes(t *testing.T) {
	bm := New(MapOfApps{
		"golangci-lint": App{
			Binary: &AppConfigBinary{Version: "v2.7.2"},
		},
		"yamllint": App{
			Uv: &AppConfigUV{PackageName: "yamllint", Version: "1.38.0"},
		},
		"mmdc": App{
			Fnm: &AppConfigFNM{
				PackageName: "@mermaid-js/mermaid-cli",
				Version:     "11.12.0",
				BinPath:     "node_modules/.bin/mmdc",
			},
		},
		"echo": App{
			Shell: &AppConfigShell{Name: "echo"},
		},
	}, nil, nil)

	apps := bm.GetAppsList()
	if len(apps) != 4 {
		t.Fatalf("expected 4 apps, got %d", len(apps))
	}

	byName := make(map[string]AppInfo)
	for _, app := range apps {
		byName[app.Name] = app
	}

	if byName["golangci-lint"].Version != "v2.7.2" {
		t.Errorf("golangci-lint version = %q, want 'v2.7.2'", byName["golangci-lint"].Version)
	}
	if byName["yamllint"].PackageName != "yamllint" {
		t.Errorf("yamllint packageName = %q, want 'yamllint'", byName["yamllint"].PackageName)
	}
	if byName["mmdc"].PackageName != "@mermaid-js/mermaid-cli" {
		t.Errorf("mmdc packageName = %q, want '@mermaid-js/mermaid-cli'", byName["mmdc"].PackageName)
	}
	if byName["echo"].Command != "echo" {
		t.Errorf("echo command = %q, want 'echo'", byName["echo"].Command)
	}
}

func TestAppConfigFNM_JSONRoundTrip(t *testing.T) {
	original := App{
		Required: true,
		Fnm: &AppConfigFNM{
			PackageName:  "@mermaid-js/mermaid-cli",
			Version:      "11.12.0",

			BinPath:      "node_modules/.bin/mmdc",
			Runtime:      "fnm",
			LockFile:     "lockfile: content",
			Dependencies: map[string]string{"playwright": "1.52.0"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded App
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.Fnm == nil {
		t.Fatal("decoded.Fnm is nil after round-trip")
	}
	if decoded.Fnm.PackageName != original.Fnm.PackageName {
		t.Errorf("PackageName = %q, want %q", decoded.Fnm.PackageName, original.Fnm.PackageName)
	}
	if decoded.Fnm.BinPath != original.Fnm.BinPath {
		t.Errorf("BinPath = %q, want %q", decoded.Fnm.BinPath, original.Fnm.BinPath)
	}
	if decoded.Fnm.Dependencies["playwright"] != "1.52.0" {
		t.Errorf("Dependencies[playwright] = %q, want %q", decoded.Fnm.Dependencies["playwright"], "1.52.0")
	}
	if decoded.Binary != nil || decoded.Uv != nil || decoded.Shell != nil {
		t.Error("expected other config fields to be nil after FNM-only round-trip")
	}
}

func TestAppConfigFNM_JSONOmitsEmpty(t *testing.T) {
	app := App{
		Fnm: &AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
		},
	}

	data, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw error: %v", err)
	}

	if _, ok := raw["binary"]; ok {
		t.Error("expected binary to be omitted")
	}
	if _, ok := raw["uv"]; ok {
		t.Error("expected uv to be omitted")
	}
	if _, ok := raw["shell"]; ok {
		t.Error("expected shell to be omitted")
	}
	if _, ok := raw["fnm"]; !ok {
		t.Error("expected fnm to be present")
	}
}

func TestApp_FilesAndLinks_JSONRoundTrip(t *testing.T) {
	original := App{
		Required: true,
		Fnm: &AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
		},
		Files: map[string]string{
			"eslint-base.js":    "module.exports = { rules: {} };",
			"prettier-base.js":  "module.exports = {};",
		},
		Links: map[string]string{
			"eslint-base.js":   "eslint-base.js",
			"prettier-base.js": "prettier-base.js",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded App
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(decoded.Files) != 2 {
		t.Errorf("Files length = %d, want 2", len(decoded.Files))
	}
	if decoded.Files["eslint-base.js"] != "module.exports = { rules: {} };" {
		t.Errorf("Files[eslint-base.js] = %q, want %q", decoded.Files["eslint-base.js"], "module.exports = { rules: {} };")
	}
	if decoded.Files["prettier-base.js"] != "module.exports = {};" {
		t.Errorf("Files[prettier-base.js] = %q, want %q", decoded.Files["prettier-base.js"], "module.exports = {};")
	}

	if len(decoded.Links) != 2 {
		t.Errorf("Links length = %d, want 2", len(decoded.Links))
	}
	if decoded.Links["eslint-base.js"] != "eslint-base.js" {
		t.Errorf("Links[eslint-base.js] = %q, want %q", decoded.Links["eslint-base.js"], "eslint-base.js")
	}
	if decoded.Links["prettier-base.js"] != "prettier-base.js" {
		t.Errorf("Links[prettier-base.js] = %q, want %q", decoded.Links["prettier-base.js"], "prettier-base.js")
	}
}

func TestApp_FilesAndLinks_OmittedWhenEmpty(t *testing.T) {
	app := App{
		Fnm: &AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
		},
	}

	data, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw error: %v", err)
	}

	if _, ok := raw["files"]; ok {
		t.Error("expected files to be omitted when nil")
	}
	if _, ok := raw["links"]; ok {
		t.Error("expected links to be omitted when nil")
	}
}

func TestApp_FilesWithoutLinks_JSONRoundTrip(t *testing.T) {
	original := App{
		Fnm: &AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
		},
		Files: map[string]string{
			"config.js": "module.exports = {};",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded App
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(decoded.Files) != 1 {
		t.Errorf("Files length = %d, want 1", len(decoded.Files))
	}
	if decoded.Links != nil {
		t.Error("expected Links to be nil when not set")
	}
}

func TestAppConfigFNM_LockFile_JSONRoundTrip(t *testing.T) {
	original := App{
		Fnm: &AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
			LockFile:    "lockfileVersion: '9.0'\npackages:\n  eslint@9.0.0:\n    resolution: {integrity: sha512-abc}",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded App
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.Fnm.LockFile != original.Fnm.LockFile {
		t.Errorf("LockFile = %q, want %q", decoded.Fnm.LockFile, original.Fnm.LockFile)
	}
}

func TestAppConfigUV_LockFile_JSONRoundTrip(t *testing.T) {
	original := App{
		Uv: &AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.38.0",
			LockFile:    "version = 1\nrequires-python = \">=3.8\"\n\n[[package]]\nname = \"yamllint\"",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded App
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.Uv.LockFile != original.Uv.LockFile {
		t.Errorf("LockFile = %q, want %q", decoded.Uv.LockFile, original.Uv.LockFile)
	}
}

func TestAppConfigLockFile_OmittedWhenEmpty(t *testing.T) {
	app := App{
		Fnm: &AppConfigFNM{
			PackageName: "eslint",
			Version:     "9.0.0",

			BinPath:     "node_modules/.bin/eslint",
		},
		Uv: &AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.38.0",
		},
	}

	data, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, "lockFile") {
		t.Error("lockFile should be omitted when empty")
	}
}

func TestGetCommandInfo_AppNotFound(t *testing.T) {
	bm := New(MapOfApps{}, nil, nil)
	_, err := bm.GetCommandInfo("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent app")
	}
}

func TestGetCommandInfo_RuntimeManagerError(t *testing.T) {
	mock := &mockRuntimeAppManager{
		getCommandInfoFunc: func(appName string, app App) (*CommandInfo, error) {
			return nil, fmt.Errorf("runtime not found")
		},
	}

	bm := New(MapOfApps{
		"yamllint": App{
			Uv: &AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
	}, nil, mock)

	_, err := bm.GetCommandInfo("yamllint")
	if err == nil {
		t.Fatal("expected error from runtime manager")
	}
}

func TestGetCommandInfo_NoValidConfig(t *testing.T) {
	bm := New(MapOfApps{
		"empty": App{},
	}, nil, nil)

	_, err := bm.GetCommandInfo("empty")
	if err == nil {
		t.Fatal("expected error for app with no valid configuration")
	}
}

func TestComputeInstallPath_BinaryApp(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("failed to get OS type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("failed to get arch type: %v", err)
	}
	libc := string(target.DetectHost().Libc)

	bm := New(MapOfApps{
		"testbin": App{
			Required: true,
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					osType: {
						archType: {libc: BinaryOsArchInfo{
							URL:         "https://example.com/testbin",
							Hash:        "abc123",
							ContentType: BinContentTypeBinary,
						}},
					},
				},
			},
		},
	}, nil, nil)

	path, err := bm.ComputeInstallPath("testbin")
	if err != nil {
		t.Fatalf("ComputeInstallPath() error = %v", err)
	}

	if path == "" {
		t.Error("expected non-empty path")
	}

	expectedPath, err := bm.getBinaryPath("testbin")
	if err != nil {
		t.Fatalf("getBinaryPath() error = %v", err)
	}
	if path != expectedPath {
		t.Errorf("ComputeInstallPath() = %q, want %q", path, expectedPath)
	}
}

func TestComputeInstallPath_RuntimeApp(t *testing.T) {
	mock := &mockRuntimeAppManager{
		computeAppPathFunc: func(appName string, app App) (string, error) {
			return "/mock/apps/uv/yamllint/hash123", nil
		},
	}

	bm := New(MapOfApps{
		"yamllint": App{
			Uv: &AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
	}, nil, mock)

	path, err := bm.ComputeInstallPath("yamllint")
	if err != nil {
		t.Fatalf("ComputeInstallPath() error = %v", err)
	}
	if path != "/mock/apps/uv/yamllint/hash123" {
		t.Errorf("ComputeInstallPath() = %q, want %q", path, "/mock/apps/uv/yamllint/hash123")
	}
}

func TestComputeInstallPath_NoRuntimeManager(t *testing.T) {
	bm := New(MapOfApps{
		"yamllint": App{
			Uv: &AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
	}, nil, nil)

	_, err := bm.ComputeInstallPath("yamllint")
	if err == nil {
		t.Fatal("expected error when no runtime manager configured")
	}
}

func TestComputeInstallPath_AppNotFound(t *testing.T) {
	bm := New(MapOfApps{}, nil, nil)
	_, err := bm.ComputeInstallPath("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent app")
	}
}

func TestComputeInstallPath_NoValidConfig(t *testing.T) {
	bm := New(MapOfApps{
		"empty": App{},
	}, nil, nil)

	_, err := bm.ComputeInstallPath("empty")
	if err == nil {
		t.Fatal("expected error for app with no config")
	}
}

func TestGetInstallRoot_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("failed to get OS type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("failed to get arch type: %v", err)
	}
	libc := string(target.DetectHost().Libc)

	bm := New(MapOfApps{
		"testbin": App{
			Required: true,
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					osType: {
						archType: {libc: BinaryOsArchInfo{
							URL:         "https://example.com/testbin",
							Hash:        "abc123",
							ContentType: BinContentTypeBinary,
						}},
					},
				},
			},
		},
	}, nil, nil)

	expectedPath, _ := bm.ComputeInstallPath("testbin")
	if err := os.MkdirAll(expectedPath, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	path, err := bm.GetInstallRoot("testbin")
	if err != nil {
		t.Fatalf("GetInstallRoot() error = %v", err)
	}
	if path != expectedPath {
		t.Errorf("GetInstallRoot() = %q, want %q", path, expectedPath)
	}
}

func TestGetInstallRoot_NotInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("failed to get OS type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("failed to get arch type: %v", err)
	}
	libc := string(target.DetectHost().Libc)

	bm := New(MapOfApps{
		"testbin": App{
			Required: true,
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					osType: {
						archType: {libc: BinaryOsArchInfo{
							URL:         "https://example.com/testbin",
							Hash:        "abc123",
							ContentType: BinContentTypeBinary,
						}},
					},
				},
			},
		},
	}, nil, nil)

	_, err = bm.GetInstallRoot("testbin")
	if err == nil {
		t.Fatal("expected error for app not installed")
	}
}

func TestGetExecCmd_BinaryApp(t *testing.T) {
	testContent := []byte("#!/bin/sh\necho hello\n")
	hash := sha256.Sum256(testContent)
	expectedHash := hex.EncodeToString(hash[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", tmpDir)

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("failed to get OS type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("failed to get arch type: %v", err)
	}
	libc := string(target.DetectHost().Libc)

	bm := New(MapOfApps{
		"testbin": App{
			Required: true,
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					osType: {
						archType: {libc: BinaryOsArchInfo{
							URL:         server.URL,
							Hash:        expectedHash,
							ContentType: BinContentTypeBinary,
						}},
					},
				},
			},
		},
	}, nil, nil)

	cmd, err := bm.GetExecCmd("testbin", []string{"--version"})
	if err != nil {
		t.Fatalf("GetExecCmd() error = %v", err)
	}
	if cmd == nil {
		t.Fatal("GetExecCmd() returned nil cmd for binary app")
	}

	expectedBinPath, err := bm.getBinaryPath("testbin")
	if err != nil {
		t.Fatalf("getBinaryPath() error = %v", err)
	}
	if cmd.Path != expectedBinPath {
		t.Errorf("cmd.Path = %q, want %q", cmd.Path, expectedBinPath)
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "--version" {
		t.Errorf("cmd.Args = %v, want [%s --version]", cmd.Args, expectedBinPath)
	}
	if cmd.Stdout != nil {
		t.Error("expected cmd.Stdout to be nil")
	}
	if cmd.Stderr != nil {
		t.Error("expected cmd.Stderr to be nil")
	}
}

func TestGetExecCmd_ShellApp_ReturnsNil(t *testing.T) {
	bm := New(MapOfApps{
		"myshell": App{
			Shell: &AppConfigShell{
				Name: "bash",
				Args: []string{"-c", "echo hello"},
			},
		},
	}, nil, nil)

	cmd, err := bm.GetExecCmd("myshell", []string{"--version"})
	if err != nil {
		t.Fatalf("GetExecCmd() error = %v", err)
	}
	if cmd != nil {
		t.Error("expected nil cmd for shell app")
	}
}

func TestGetExecCmd_RuntimeApp(t *testing.T) {
	mock := &mockRuntimeAppManager{
		getCommandInfoFunc: func(appName string, app App) (*CommandInfo, error) {
			return &CommandInfo{
				Type:    "uv",
				Command: "/mock/bin/yamllint",
				Env:     map[string]string{"UV_TOOL_DIR": "/mock/tools"},
			}, nil
		},
	}

	bm := New(MapOfApps{
		"yamllint": App{
			Uv: &AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.38.0",
			},
		},
	}, nil, mock)

	cmd, err := bm.GetExecCmd("yamllint", []string{"--version"})
	if err != nil {
		t.Fatalf("GetExecCmd() error = %v", err)
	}
	if cmd == nil {
		t.Fatal("GetExecCmd() returned nil cmd for uv app")
	}
	if cmd.Path != "/mock/bin/yamllint" {
		t.Errorf("cmd.Path = %q, want %q", cmd.Path, "/mock/bin/yamllint")
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "--version" {
		t.Errorf("cmd.Args = %v, want [/mock/bin/yamllint --version]", cmd.Args)
	}
}

func TestGetExecCmd_AppNotFound(t *testing.T) {
	bm := New(MapOfApps{}, nil, nil)
	_, err := bm.GetExecCmd("nonexistent", []string{"--version"})
	if err == nil {
		t.Fatal("expected error for nonexistent app")
	}
}

func TestWriteAppFiles_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	files := map[string]string{
		"config.js":  "module.exports = { rules: {} };",
		"base.json":  `{"extends": "recommended"}`,
	}

	err := WriteAppFiles(installPath, files, nil)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	for filename, expectedContent := range files {
		content, err := os.ReadFile(fmt.Sprintf("%s/%s", installPath, filename))
		if err != nil {
			t.Fatalf("failed to read file %q: %v", filename, err)
		}
		if string(content) != expectedContent {
			t.Errorf("file %q content = %q, want %q", filename, string(content), expectedContent)
		}
	}
}

func TestWriteAppFiles_EmptyMap(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	err := WriteAppFiles(installPath, map[string]string{}, nil)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	if _, err := os.Stat(installPath); err != nil {
		t.Errorf("expected directory to be created even with empty files map")
	}
}

func TestWriteAppFiles_OverwritesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)
	if err := os.MkdirAll(installPath, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	filePath := fmt.Sprintf("%s/config.js", installPath)
	if err := os.WriteFile(filePath, []byte("old content"), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	err := WriteAppFiles(installPath, map[string]string{
		"config.js": "new content",
	}, nil)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "new content" {
		t.Errorf("content = %q, want %q", string(content), "new content")
	}
}

func TestWriteAppFiles_CreatesParentDirs(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/deep/nested/app-install", tmpDir)

	err := WriteAppFiles(installPath, map[string]string{
		"config.js": "content",
	}, nil)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	content, err := os.ReadFile(fmt.Sprintf("%s/config.js", installPath))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "content" {
		t.Errorf("content = %q, want %q", string(content), "content")
	}
}

func TestWriteAppFiles_WithInlineArchive(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	tarData := createTestTarData(t, map[string]string{
		"config.js":     "module.exports = {};",
		"sub/nested.js": "export default 42;",
	})
	compressed, err := CompressArchive(tarData)
	if err != nil {
		t.Fatalf("CompressArchive() error = %v", err)
	}

	archives := map[string]*ArchiveSpec{
		"configs": {Inline: compressed},
	}

	err = WriteAppFiles(installPath, nil, archives)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	content, err := os.ReadFile(fmt.Sprintf("%s/config.js", installPath))
	if err != nil {
		t.Fatalf("failed to read config.js: %v", err)
	}
	if string(content) != "module.exports = {};" {
		t.Errorf("config.js content = %q, want %q", string(content), "module.exports = {};")
	}

	content, err = os.ReadFile(fmt.Sprintf("%s/sub/nested.js", installPath))
	if err != nil {
		t.Fatalf("failed to read sub/nested.js: %v", err)
	}
	if string(content) != "export default 42;" {
		t.Errorf("sub/nested.js content = %q, want %q", string(content), "export default 42;")
	}
}

func TestWriteAppFiles_FilesOverwriteArchives(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	tarData := createTestTarData(t, map[string]string{
		"config.js": "from archive",
	})
	compressed, err := CompressArchive(tarData)
	if err != nil {
		t.Fatalf("CompressArchive() error = %v", err)
	}

	archives := map[string]*ArchiveSpec{
		"configs": {Inline: compressed},
	}
	files := map[string]string{
		"config.js": "from files",
	}

	err = WriteAppFiles(installPath, files, archives)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	content, err := os.ReadFile(fmt.Sprintf("%s/config.js", installPath))
	if err != nil {
		t.Fatalf("failed to read config.js: %v", err)
	}
	if string(content) != "from files" {
		t.Errorf("config.js content = %q, want %q (files should override archives)", string(content), "from files")
	}
}

func TestWriteAppFiles_ArchivesExtractedAlphabetically(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	// Archive "alpha" contains config.js with "from alpha"
	tarDataAlpha := createTestTarData(t, map[string]string{
		"config.js": "from alpha",
		"alpha.txt": "only in alpha",
	})
	compressedAlpha, err := CompressArchive(tarDataAlpha)
	if err != nil {
		t.Fatalf("CompressArchive(alpha) error = %v", err)
	}

	// Archive "beta" contains config.js with "from beta" (should overwrite alpha's)
	tarDataBeta := createTestTarData(t, map[string]string{
		"config.js": "from beta",
		"beta.txt":  "only in beta",
	})
	compressedBeta, err := CompressArchive(tarDataBeta)
	if err != nil {
		t.Fatalf("CompressArchive(beta) error = %v", err)
	}

	archives := map[string]*ArchiveSpec{
		"beta":  {Inline: compressedBeta},
		"alpha": {Inline: compressedAlpha},
	}

	err = WriteAppFiles(installPath, nil, archives)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	// config.js should have "from beta" because beta sorts after alpha
	content, err := os.ReadFile(fmt.Sprintf("%s/config.js", installPath))
	if err != nil {
		t.Fatalf("failed to read config.js: %v", err)
	}
	if string(content) != "from beta" {
		t.Errorf("config.js content = %q, want %q (later archive alphabetically should overwrite)", string(content), "from beta")
	}

	// Both unique files should exist
	content, err = os.ReadFile(fmt.Sprintf("%s/alpha.txt", installPath))
	if err != nil {
		t.Fatalf("failed to read alpha.txt: %v", err)
	}
	if string(content) != "only in alpha" {
		t.Errorf("alpha.txt content = %q, want %q", string(content), "only in alpha")
	}

	content, err = os.ReadFile(fmt.Sprintf("%s/beta.txt", installPath))
	if err != nil {
		t.Fatalf("failed to read beta.txt: %v", err)
	}
	if string(content) != "only in beta" {
		t.Errorf("beta.txt content = %q, want %q", string(content), "only in beta")
	}
}

func TestWriteAppFiles_ArchivesBeforeFiles(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	// Archive contains two files
	tarData := createTestTarData(t, map[string]string{
		"shared.js":       "from archive",
		"archive-only.js": "archive content",
	})
	compressed, err := CompressArchive(tarData)
	if err != nil {
		t.Fatalf("CompressArchive() error = %v", err)
	}

	archives := map[string]*ArchiveSpec{
		"base": {Inline: compressed},
	}
	files := map[string]string{
		"shared.js":     "from files",
		"files-only.js": "files content",
	}

	err = WriteAppFiles(installPath, files, archives)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	// shared.js should have "from files" (files applied after archives)
	content, err := os.ReadFile(fmt.Sprintf("%s/shared.js", installPath))
	if err != nil {
		t.Fatalf("failed to read shared.js: %v", err)
	}
	if string(content) != "from files" {
		t.Errorf("shared.js = %q, want %q (files should override archives)", string(content), "from files")
	}

	// archive-only.js should exist from archive
	content, err = os.ReadFile(fmt.Sprintf("%s/archive-only.js", installPath))
	if err != nil {
		t.Fatalf("failed to read archive-only.js: %v", err)
	}
	if string(content) != "archive content" {
		t.Errorf("archive-only.js = %q, want %q", string(content), "archive content")
	}

	// files-only.js should exist from files
	content, err = os.ReadFile(fmt.Sprintf("%s/files-only.js", installPath))
	if err != nil {
		t.Fatalf("failed to read files-only.js: %v", err)
	}
	if string(content) != "files content" {
		t.Errorf("files-only.js = %q, want %q", string(content), "files content")
	}
}

func TestWriteAppFiles_ExternalArchiveWithServer(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	tarData := createTestTarData(t, map[string]string{
		"hello.txt": "hello world",
	})

	hash := sha256.Sum256(tarData)
	hashHex := hex.EncodeToString(hash[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarData)
	}))
	defer server.Close()

	archives := map[string]*ArchiveSpec{
		"remote": {
			URL:    server.URL + "/test.tar",
			Hash:   hashHex,
			Format: BinContentTypeTar,
		},
	}

	err := WriteAppFiles(installPath, nil, archives)
	if err != nil {
		t.Fatalf("WriteAppFiles() error = %v", err)
	}

	content, err := os.ReadFile(fmt.Sprintf("%s/hello.txt", installPath))
	if err != nil {
		t.Fatalf("failed to read hello.txt: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("hello.txt content = %q, want %q", string(content), "hello world")
	}
}

func TestWriteAppFiles_ExternalArchiveBadHash(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	tarData := createTestTarData(t, map[string]string{
		"hello.txt": "hello world",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarData)
	}))
	defer server.Close()

	archives := map[string]*ArchiveSpec{
		"remote": {
			URL:    server.URL + "/test.tar",
			Hash:   "0000000000000000000000000000000000000000000000000000000000000000",
			Format: BinContentTypeTar,
		},
	}

	err := WriteAppFiles(installPath, nil, archives)
	if err == nil {
		t.Fatal("expected error for hash mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "hash verification failed") {
		t.Errorf("expected hash verification error, got: %v", err)
	}
}

func TestWriteAppFiles_InvalidArchiveSpec(t *testing.T) {
	tmpDir := t.TempDir()
	installPath := fmt.Sprintf("%s/app-install", tmpDir)

	archives := map[string]*ArchiveSpec{
		"bad": {},
	}

	err := WriteAppFiles(installPath, nil, archives)
	if err == nil {
		t.Fatal("expected error for empty archive spec, got nil")
	}
	if !strings.Contains(err.Error(), "must have either inline or url") {
		t.Errorf("expected validation error, got: %v", err)
	}
}

func createTestTarData(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar write header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

func TestNestedStorageParsing(t *testing.T) {
	jsonData := `{
		"testapp": {
			"binary": {
				"binaries": {
					"linux": {
						"amd64": {
							"glibc": {"url": "https://example.com/linux-amd64-glibc", "hash": "abc123", "contentType": "binary"},
							"musl": {"url": "https://example.com/linux-amd64-musl", "hash": "def456", "contentType": "binary"}
						}
					},
					"darwin": {
						"arm64": {
							"unknown": {"url": "https://example.com/darwin-arm64", "hash": "ghi789", "contentType": "binary"}
						}
					}
				}
			}
		}
	}`

	var apps MapOfApps
	if err := json.Unmarshal([]byte(jsonData), &apps); err != nil {
		t.Fatalf("failed to parse nested storage JSON: %v", err)
	}

	app, ok := apps["testapp"]
	if !ok {
		t.Fatal("expected testapp in parsed apps")
	}
	if app.Binary == nil {
		t.Fatal("expected binary config")
	}

	// Linux glibc entry
	linuxMap, ok := app.Binary.Binaries["linux"]
	if !ok {
		t.Fatal("expected linux in binaries")
	}
	amd64Map, ok := linuxMap["amd64"]
	if !ok {
		t.Fatal("expected amd64 in linux binaries")
	}
	glibcInfo, ok := amd64Map["glibc"]
	if !ok {
		t.Fatal("expected glibc in linux/amd64 binaries")
	}
	if glibcInfo.URL != "https://example.com/linux-amd64-glibc" {
		t.Errorf("expected glibc URL, got %s", glibcInfo.URL)
	}

	// Linux musl entry
	muslInfo, ok := amd64Map["musl"]
	if !ok {
		t.Fatal("expected musl in linux/amd64 binaries")
	}
	if muslInfo.URL != "https://example.com/linux-amd64-musl" {
		t.Errorf("expected musl URL, got %s", muslInfo.URL)
	}

	// Darwin unknown entry
	darwinMap, ok := app.Binary.Binaries["darwin"]
	if !ok {
		t.Fatal("expected darwin in binaries")
	}
	arm64Map, ok := darwinMap["arm64"]
	if !ok {
		t.Fatal("expected arm64 in darwin binaries")
	}
	unknownInfo, ok := arm64Map["unknown"]
	if !ok {
		t.Fatal("expected unknown in darwin/arm64 binaries")
	}
	if unknownInfo.URL != "https://example.com/darwin-arm64" {
		t.Errorf("expected darwin URL, got %s", unknownInfo.URL)
	}
}

func TestNestedStorageNonLinuxUsesUnknownLibc(t *testing.T) {
	apps := MapOfApps{
		"testbin": App{
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					"darwin": {
						"amd64": {"unknown": BinaryOsArchInfo{
							URL: "https://example.com/darwin-amd64", Hash: "abc", ContentType: BinContentTypeBinary,
						}},
						"arm64": {"unknown": BinaryOsArchInfo{
							URL: "https://example.com/darwin-arm64", Hash: "def", ContentType: BinContentTypeBinary,
						}},
					},
					"windows": {
						"amd64": {"unknown": BinaryOsArchInfo{
							URL: "https://example.com/windows-amd64", Hash: "ghi", ContentType: BinContentTypeBinary,
						}},
					},
					"freebsd": {
						"amd64": {"unknown": BinaryOsArchInfo{
							URL: "https://example.com/freebsd-amd64", Hash: "jkl", ContentType: BinContentTypeBinary,
						}},
					},
				},
			},
		},
	}

	for _, tc := range []struct {
		os   string
		arch string
	}{
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
		{"freebsd", "amd64"},
	} {
		resolver := target.NewResolver(target.Target{OS: tc.os, Arch: tc.arch, Libc: target.LibcUnknown})
		bm := NewWithResolver(apps, nil, nil, resolver)
		resolved, info, err := bm.getBinaryInfo("testbin")
		if err != nil {
			t.Errorf("getBinaryInfo(testbin) for %s/%s error: %v", tc.os, tc.arch, err)
			continue
		}
		if info.URL == "" {
			t.Errorf("getBinaryInfo(testbin) for %s/%s returned empty URL", tc.os, tc.arch)
		}
		if resolved.Source != target.ResolutionExact {
			t.Errorf("expected exact resolution for %s/%s, got fallback", tc.os, tc.arch)
		}
	}

	// Host with glibc on darwin should still resolve via fallback to unknown
	resolver := target.NewResolver(target.Target{OS: "darwin", Arch: "amd64", Libc: target.LibcGlibc})
	bm := NewWithResolver(apps, nil, nil, resolver)
	resolved, _, err := bm.getBinaryInfo("testbin")
	if err != nil {
		t.Fatalf("expected fallback resolution for darwin/amd64/glibc, got error: %v", err)
	}
	if resolved.Source != target.ResolutionFallback {
		t.Error("expected fallback for darwin/amd64/glibc host with unknown candidate")
	}
}

func TestNestedStorageLinuxLibcLookup(t *testing.T) {
	apps := MapOfApps{
		"testbin": App{
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					"linux": {
						"amd64": {
							"glibc": BinaryOsArchInfo{
								URL: "https://example.com/linux-amd64-glibc", Hash: "abc", ContentType: BinContentTypeBinary,
							},
							"musl": BinaryOsArchInfo{
								URL: "https://example.com/linux-amd64-musl", Hash: "def", ContentType: BinContentTypeBinary,
							},
						},
					},
				},
			},
		},
	}

	// Glibc host resolves to glibc exact
	glibcResolver := target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcGlibc})
	bm := NewWithResolver(apps, nil, nil, glibcResolver)
	resolved, info, err := bm.getBinaryInfo("testbin")
	if err != nil {
		t.Fatalf("getBinaryInfo(glibc host) error: %v", err)
	}
	if info.URL != "https://example.com/linux-amd64-glibc" {
		t.Errorf("expected glibc URL, got %s", info.URL)
	}
	if resolved.Source != target.ResolutionExact {
		t.Error("expected exact resolution for glibc host")
	}

	// Musl host resolves to musl exact
	muslResolver := target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcMusl})
	bm = NewWithResolver(apps, nil, nil, muslResolver)
	resolved, info, err = bm.getBinaryInfo("testbin")
	if err != nil {
		t.Fatalf("getBinaryInfo(musl host) error: %v", err)
	}
	if info.URL != "https://example.com/linux-amd64-musl" {
		t.Errorf("expected musl URL, got %s", info.URL)
	}
	if resolved.Source != target.ResolutionExact {
		t.Error("expected exact resolution for musl host")
	}

	// Unknown libc host falls back (picks one deterministically)
	unknownResolver := target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcUnknown})
	bm = NewWithResolver(apps, nil, nil, unknownResolver)
	resolved, _, err = bm.getBinaryInfo("testbin")
	if err != nil {
		t.Fatalf("getBinaryInfo(unknown host) error: %v", err)
	}
	if resolved.Source != target.ResolutionFallback {
		t.Error("expected fallback resolution for unknown libc host")
	}
}

func TestNestedStorageJSONRoundTrip(t *testing.T) {
	original := MapOfBinaries{
		"linux": {
			"amd64": {
				"glibc": BinaryOsArchInfo{URL: "https://example.com/glibc", Hash: "abc123", ContentType: BinContentTypeTarGz},
				"musl":  BinaryOsArchInfo{URL: "https://example.com/musl", Hash: "def456", ContentType: BinContentTypeTarGz},
			},
		},
		"darwin": {
			"arm64": {
				"unknown": BinaryOsArchInfo{URL: "https://example.com/darwin", Hash: "ghi789", ContentType: BinContentTypeZip},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed MapOfBinaries
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// Verify round-trip
	if parsed["linux"]["amd64"]["glibc"].URL != "https://example.com/glibc" {
		t.Error("glibc URL mismatch after round-trip")
	}
	if parsed["linux"]["amd64"]["musl"].URL != "https://example.com/musl" {
		t.Error("musl URL mismatch after round-trip")
	}
	if parsed["darwin"]["arm64"]["unknown"].URL != "https://example.com/darwin" {
		t.Error("darwin URL mismatch after round-trip")
	}
}

func TestParseBinaryCandidates(t *testing.T) {
	binaries := MapOfBinaries{
		"linux": {
			"amd64": {
				"glibc": BinaryOsArchInfo{URL: "https://example.com/glibc", Hash: "abc", ContentType: BinContentTypeBinary},
				"musl":  BinaryOsArchInfo{URL: "https://example.com/musl", Hash: "def", ContentType: BinContentTypeBinary},
			},
			"arm64": {
				"glibc": BinaryOsArchInfo{URL: "https://example.com/arm-glibc", Hash: "ghi", ContentType: BinContentTypeBinary},
			},
		},
		"darwin": {
			"arm64": {
				"unknown": BinaryOsArchInfo{URL: "https://example.com/darwin", Hash: "jkl", ContentType: BinContentTypeBinary},
			},
		},
	}

	candidates := parseBinaryCandidates(binaries)

	if len(candidates) != 4 {
		t.Fatalf("expected 4 candidates, got %d", len(candidates))
	}

	urlSet := make(map[string]bool)
	for _, c := range candidates {
		info := c.Info.(*BinaryOsArchInfo)
		urlSet[info.URL] = true

		if c.Target.OS == "" || c.Target.Arch == "" || c.Target.Libc == "" {
			t.Errorf("candidate has empty field: %v", c.Target)
		}
	}

	for _, url := range []string{
		"https://example.com/glibc",
		"https://example.com/musl",
		"https://example.com/arm-glibc",
		"https://example.com/darwin",
	} {
		if !urlSet[url] {
			t.Errorf("expected candidate with URL %s", url)
		}
	}
}

func TestGetBinaryInfoMuslExactMatch(t *testing.T) {
	apps := MapOfApps{
		"testbin": App{
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					"linux": {
						"amd64": {
							"glibc": BinaryOsArchInfo{URL: "https://example.com/glibc", Hash: "abc", ContentType: BinContentTypeBinary},
							"musl":  BinaryOsArchInfo{URL: "https://example.com/musl", Hash: "def", ContentType: BinContentTypeBinary},
						},
					},
				},
			},
		},
	}

	resolver := target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcMusl})
	bm := NewWithResolver(apps, nil, nil, resolver)

	resolved, info, err := bm.getBinaryInfo("testbin")
	if err != nil {
		t.Fatalf("getBinaryInfo() error: %v", err)
	}
	if resolved.Source != target.ResolutionExact {
		t.Error("expected exact resolution for musl host with musl candidate")
	}
	if info.URL != "https://example.com/musl" {
		t.Errorf("expected musl URL, got %s", info.URL)
	}
	if resolved.Target.Libc != target.LibcMusl {
		t.Errorf("expected musl in resolved target, got %s", resolved.Target.Libc)
	}
}

func TestGetBinaryInfoGlibcFallback(t *testing.T) {
	apps := MapOfApps{
		"testbin": App{
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					"linux": {
						"amd64": {
							"glibc": BinaryOsArchInfo{URL: "https://example.com/glibc", Hash: "abc", ContentType: BinContentTypeBinary},
						},
					},
				},
			},
		},
	}

	resolver := target.NewResolver(target.Target{OS: "linux", Arch: "amd64", Libc: target.LibcMusl})
	bm := NewWithResolver(apps, nil, nil, resolver)

	resolved, info, err := bm.getBinaryInfo("testbin")
	if err != nil {
		t.Fatalf("getBinaryInfo() error: %v", err)
	}
	if resolved.Source != target.ResolutionFallback {
		t.Error("expected fallback resolution for musl host with only glibc candidate")
	}
	if info.URL != "https://example.com/glibc" {
		t.Errorf("expected glibc URL as fallback, got %s", info.URL)
	}
	if resolved.FallbackInfo == nil {
		t.Fatal("expected FallbackInfo to be set")
	}
	if resolved.FallbackInfo.Reason == "" {
		t.Error("expected non-empty fallback reason")
	}
	if !strings.Contains(resolved.FallbackInfo.Reason, "musl") {
		t.Errorf("expected fallback reason to mention musl, got: %s", resolved.FallbackInfo.Reason)
	}
}

func TestGetBinaryInfoNoMatchReturnsError(t *testing.T) {
	apps := MapOfApps{
		"testbin": App{
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					"linux": {
						"amd64": {
							"glibc": BinaryOsArchInfo{URL: "https://example.com/glibc", Hash: "abc", ContentType: BinContentTypeBinary},
						},
					},
				},
			},
		},
	}

	resolver := target.NewResolver(target.Target{OS: "darwin", Arch: "arm64", Libc: target.LibcUnknown})
	bm := NewWithResolver(apps, nil, nil, resolver)

	_, _, err := bm.getBinaryInfo("testbin")
	if err == nil {
		t.Error("expected error when no OS/Arch match exists")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("expected 'not available' in error, got: %s", err.Error())
	}
}

func TestGetBinaryInfoNotFoundApp(t *testing.T) {
	bm := New(MapOfApps{}, nil, nil)
	_, _, err := bm.getBinaryInfo("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent app")
	}
}

func TestGetBinaryInfoNotBinaryApp(t *testing.T) {
	apps := MapOfApps{
		"uvapp": App{
			Uv: &AppConfigUV{PackageName: "yamllint", Version: "1.0"},
		},
	}
	bm := New(apps, nil, nil)
	_, _, err := bm.getBinaryInfo("uvapp")
	if err == nil {
		t.Error("expected error for non-binary app")
	}
}
