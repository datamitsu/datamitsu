package runtimemanager

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

// envContains reports whether the KEY=VALUE form built by buildEnvWithOverrides
// is present in the install-time environment slice.
func envContains(t *testing.T, result []string, want string) bool {
	t.Helper()
	return slices.Contains(result, want)
}

func TestMergeInstallEnv(t *testing.T) {
	appDir := "/cache/.apps/uv/yamllint/abc123"

	t.Run("nil custom returns reserved unchanged", func(t *testing.T) {
		reserved := map[string]string{"UV_CACHE_DIR": "/x"}
		got := mergeInstallEnv(reserved, nil, appDir)
		if len(got) != 1 || got["UV_CACHE_DIR"] != "/x" {
			t.Errorf("got %v, want reserved unchanged", got)
		}
	})

	t.Run("expands placeholders in custom values", func(t *testing.T) {
		custom := map[string]string{
			"PLAYWRIGHT_BROWSERS_PATH": "${STORE}/.playwright/browsers",
			"FOO":                      "${APP_DIR}/foo",
		}
		got := mergeInstallEnv(map[string]string{}, custom, appDir)
		// ExpandPlaceholders does plain string replacement, not path join.
		if got["PLAYWRIGHT_BROWSERS_PATH"] != env.GetStorePath()+"/.playwright/browsers" {
			t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q, want %q", got["PLAYWRIGHT_BROWSERS_PATH"], env.GetStorePath()+"/.playwright/browsers")
		}
		if got["FOO"] != appDir+"/foo" {
			t.Errorf("FOO = %q, want %q", got["FOO"], appDir+"/foo")
		}
	})

	t.Run("reserved runtime keys win over custom", func(t *testing.T) {
		reserved := map[string]string{"UV_CACHE_DIR": "/reserved/cache"}
		custom := map[string]string{"UV_CACHE_DIR": "/user/cache"}
		got := mergeInstallEnv(reserved, custom, appDir)
		if got["UV_CACHE_DIR"] != "/reserved/cache" {
			t.Errorf("UV_CACHE_DIR = %q, want reserved value to win", got["UV_CACHE_DIR"])
		}
	})
}

func TestInstallTimeEnvUVCustom(t *testing.T) {
	appEnvPath := "/cache/.apps/uv/yamllint/abc123"
	custom := map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "${STORE}/.playwright/browsers"}

	merged := mergeInstallEnv(getUVEnvVars("/runtime-data/uv/identity"), custom, appEnvPath)
	result := buildEnvWithOverrides(os.Environ(), merged)

	want := "PLAYWRIGHT_BROWSERS_PATH=" + env.GetStorePath() + "/.playwright/browsers"
	if !envContains(t, result, want) {
		t.Errorf("uv install-time env missing %q", want)
	}

	// Reserved key wins even if the user tries to set it.
	collide := mergeInstallEnv(getUVEnvVars("/runtime-data/uv/identity"), map[string]string{"UV_PYTHON_INSTALL_DIR": "/evil"}, appEnvPath)
	if collide["UV_PYTHON_INSTALL_DIR"] == "/evil" {
		t.Error("uv: custom env must not override reserved UV_PYTHON_INSTALL_DIR")
	}
}

func TestInstallTimeEnvNodeCustom(t *testing.T) {
	appEnvPath := "/cache/.apps/node/eslint/abc123"
	custom := map[string]string{"FOO": "${APP_DIR}/bar"}

	reserved := getPNPMEnvVars(appEnvPath, "/runtime-data/pnpm/store", "/runtime-data/node/identity")
	reserved["PATH"] = "/managed/node/bin"
	merged := mergeInstallEnv(reserved, custom, appEnvPath)
	result := buildEnvWithOverrides(os.Environ(), merged)

	want := "FOO=" + appEnvPath + "/bar"
	if !envContains(t, result, want) {
		t.Errorf("node install-time env missing %q", want)
	}

	// Reserved PATH must win.
	collide := mergeInstallEnv(reserved, map[string]string{"PATH": "/evil"}, appEnvPath)
	if collide["PATH"] == "/evil" {
		t.Error("node: custom env must not override reserved PATH")
	}
}

func TestInstallTimeEnvGoCustom(t *testing.T) {
	appEnvPath := "/cache/.apps/go/govulncheck/abc123"
	custom := map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "${STORE}/.playwright/browsers"}

	merged := mergeInstallEnv(getGoEnvVars(appEnvPath, "/runtime-data/go/identity"), custom, appEnvPath)
	result := buildEnvWithOverrides(os.Environ(), merged)

	want := "PLAYWRIGHT_BROWSERS_PATH=" + env.GetStorePath() + "/.playwright/browsers"
	if !envContains(t, result, want) {
		t.Errorf("go install-time env missing %q", want)
	}

	// Reserved GOPATH must win.
	collide := mergeInstallEnv(getGoEnvVars(appEnvPath, "/runtime-data/go/identity"), map[string]string{"GOPATH": "/evil"}, appEnvPath)
	if collide["GOPATH"] == "/evil" {
		t.Error("go: custom env must not override reserved GOPATH")
	}
}

func TestUVRuntimeDataDirectoriesIgnoreExecutableValues(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "uv-cache")
	pythonDir := filepath.Join(root, "python")
	executable := filepath.Join(pythonDir, "bin", "python3.15")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	neverDir := filepath.Join(root, "never")
	owned := map[string]string{
		"UV_CACHE_DIR":          cacheDir,
		"UV_PYTHON_INSTALL_DIR": pythonDir,
		"UV_PYTHON":             executable,
		"UV_PYTHON_DOWNLOADS":   neverDir,
	}
	if err := ensureUVRuntimeDataDirectories(owned); err != nil {
		t.Fatalf("ensure UV runtime data directories: %v", err)
	}
	for _, dir := range []string{cacheDir, pythonDir} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("owned directory %q was not created: stat=%v err=%v", dir, info, err)
		}
	}
	if data, err := os.ReadFile(executable); err != nil || string(data) != "python" {
		t.Errorf("Python executable was changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(neverDir); !os.IsNotExist(err) {
		t.Errorf("scalar UV_PYTHON_DOWNLOADS value was created as a directory: stat error %v", err)
	}
}
