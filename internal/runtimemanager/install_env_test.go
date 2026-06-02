package runtimemanager

import (
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

// envContains reports whether the KEY=VALUE form built by buildEnvWithOverrides
// is present in the install-time environment slice.
func envContains(t *testing.T, result []string, want string) bool {
	t.Helper()
	for _, e := range result {
		if e == want {
			return true
		}
	}
	return false
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

	merged := mergeInstallEnv(getUVEnvVars(appEnvPath), custom, appEnvPath)
	result := buildEnvWithOverrides(os.Environ(), merged)

	want := "PLAYWRIGHT_BROWSERS_PATH=" + env.GetStorePath() + "/.playwright/browsers"
	if !envContains(t, result, want) {
		t.Errorf("uv install-time env missing %q", want)
	}

	// Reserved key wins even if the user tries to set it.
	collide := mergeInstallEnv(getUVEnvVars(appEnvPath), map[string]string{"UV_PYTHON_INSTALL_DIR": "/evil"}, appEnvPath)
	if collide["UV_PYTHON_INSTALL_DIR"] == "/evil" {
		t.Error("uv: custom env must not override reserved UV_PYTHON_INSTALL_DIR")
	}
}

func TestInstallTimeEnvNodeCustom(t *testing.T) {
	appEnvPath := "/cache/.apps/node/eslint/abc123"
	custom := map[string]string{"FOO": "${APP_DIR}/bar"}

	reserved := getNodeEnvVars(appEnvPath)
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

	merged := mergeInstallEnv(getGoEnvVars(appEnvPath), custom, appEnvPath)
	result := buildEnvWithOverrides(os.Environ(), merged)

	want := "PLAYWRIGHT_BROWSERS_PATH=" + env.GetStorePath() + "/.playwright/browsers"
	if !envContains(t, result, want) {
		t.Errorf("go install-time env missing %q", want)
	}

	// Reserved GOPATH must win.
	collide := mergeInstallEnv(getGoEnvVars(appEnvPath), map[string]string{"GOPATH": "/evil"}, appEnvPath)
	if collide["GOPATH"] == "/evil" {
		t.Error("go: custom env must not override reserved GOPATH")
	}
}
