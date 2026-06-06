package runtimemanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
)

func TestGetUVEnvVars(t *testing.T) {
	appEnvPath := "/cache/.apps/uv/yamllint/abc123"
	vars := getUVEnvVars(appEnvPath)

	expected := map[string]string{
		"UV_CACHE_DIR":          filepath.Join(appEnvPath, "cache"),
		"UV_PYTHON_INSTALL_DIR": filepath.Join(env.GetStorePath(), ".uv", "python"),
	}

	for key, want := range expected {
		got, ok := vars[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("vars[%q] = %q, want %q", key, got, want)
		}
	}

	if len(vars) != len(expected) {
		t.Errorf("vars has %d entries, want %d", len(vars), len(expected))
	}

	// UV_PYTHON_INSTALL_DIR must live under the store (shared), not the per-app dir.
	pyDir := vars["UV_PYTHON_INSTALL_DIR"]
	if !strings.HasPrefix(pyDir, env.GetStorePath()) {
		t.Errorf("UV_PYTHON_INSTALL_DIR = %q, want prefix %q", pyDir, env.GetStorePath())
	}
	if strings.HasPrefix(pyDir, appEnvPath) {
		t.Errorf("UV_PYTHON_INSTALL_DIR = %q should be shared, not under appEnvPath %q", pyDir, appEnvPath)
	}
}

func TestInstallTimeEnvIncludesPythonInstallDir(t *testing.T) {
	appEnvPath := "/cache/.apps/uv/yamllint/abc123"
	envVars := getUVEnvVars(appEnvPath)
	result := buildEnvWithOverrides(os.Environ(), envVars)

	wantPrefix := "UV_PYTHON_INSTALL_DIR=" + filepath.Join(env.GetStorePath(), ".uv", "python")
	found := slices.Contains(result, wantPrefix)
	if !found {
		t.Errorf("install-time env missing %q", wantPrefix)
	}
}

func TestGetUVBinaryPath(t *testing.T) {
	t.Run("simple package name", func(t *testing.T) {
		path := getUVBinaryPath("/cache/.apps/uv/yamllint/abc123", "yamllint")
		want := filepath.Join("/cache/.apps/uv/yamllint/abc123", ".venv", "bin", "yamllint")
		if runtime.GOOS == "windows" {
			want = filepath.Join("/cache/.apps/uv/yamllint/abc123", ".venv", "Scripts", "yamllint.exe")
		}
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})

	t.Run("different package name", func(t *testing.T) {
		path := getUVBinaryPath("/cache/.apps/uv/ruff/def456", "ruff")
		want := filepath.Join("/cache/.apps/uv/ruff/def456", ".venv", "bin", "ruff")
		if runtime.GOOS == "windows" {
			want = filepath.Join("/cache/.apps/uv/ruff/def456", ".venv", "Scripts", "ruff.exe")
		}
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})
}

func TestBuildEnvWithOverrides(t *testing.T) {
	t.Run("adds new variables", func(t *testing.T) {
		base := []string{"PATH=/usr/bin", "HOME=/home/user"}
		overrides := map[string]string{
			"UV_TOOL_DIR": "/tmp/tools",
		}

		result := buildEnvWithOverrides(base, overrides)

		found := slices.Contains(result, "UV_TOOL_DIR=/tmp/tools")
		if !found {
			t.Error("UV_TOOL_DIR not found in result")
		}
	})

	t.Run("overrides existing variables", func(t *testing.T) {
		base := []string{"UV_TOOL_DIR=/old/path", "HOME=/home/user"}
		overrides := map[string]string{
			"UV_TOOL_DIR": "/new/path",
		}

		result := buildEnvWithOverrides(base, overrides)

		count := 0
		for _, e := range result {
			if e == "UV_TOOL_DIR=/new/path" {
				count++
			}
			if e == "UV_TOOL_DIR=/old/path" {
				t.Error("old value should be replaced")
			}
		}
		if count != 1 {
			t.Errorf("expected exactly 1 UV_TOOL_DIR entry, got %d", count)
		}
	})

	t.Run("does not modify base slice", func(t *testing.T) {
		base := []string{"PATH=/usr/bin"}
		overrides := map[string]string{"NEW_VAR": "value"}

		buildEnvWithOverrides(base, overrides)

		if len(base) != 1 {
			t.Error("base slice was modified")
		}
	})

	t.Run("multiple overrides", func(t *testing.T) {
		base := []string{"A=1", "B=2", "C=3"}
		overrides := map[string]string{
			"A": "10",
			"D": "4",
		}

		result := buildEnvWithOverrides(base, overrides)

		expectedEntries := map[string]bool{
			"A=10": false,
			"B=2":  false,
			"C=3":  false,
			"D=4":  false,
		}
		for _, e := range result {
			if _, ok := expectedEntries[e]; ok {
				expectedEntries[e] = true
			}
		}
		for entry, found := range expectedEntries {
			if !found {
				t.Errorf("expected entry %q not found in result", entry)
			}
		}
	})
}

func TestGetUVCommandInfo(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	t.Run("returns correct command info", func(t *testing.T) {
		appConfig := &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.37.0",
			Runtime:     "uv",
		}

		info, err := rm.GetUVCommandInfo("yamllint", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("GetUVCommandInfo() error = %v", err)
		}

		if info.Type != "uv" {
			t.Errorf("Type = %q, want %q", info.Type, "uv")
		}

		if info.Command == "" {
			t.Error("Command is empty")
		}

		if info.Env == nil {
			t.Error("Env is nil")
		}

		if _, ok := info.Env["UV_CACHE_DIR"]; !ok {
			t.Error("missing env key UV_CACHE_DIR")
		}
		if _, ok := info.Env["UV_PYTHON_INSTALL_DIR"]; !ok {
			t.Error("missing env key UV_PYTHON_INSTALL_DIR")
		}
		if len(info.Env) != 2 {
			t.Errorf("expected 2 env keys, got %d", len(info.Env))
		}
	})

	t.Run("explicit runtime ref", func(t *testing.T) {
		appConfig := &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.37.0",
			Runtime:     "uv",
		}

		info, err := rm.GetUVCommandInfo("yamllint", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("GetUVCommandInfo() error = %v", err)
		}

		if info.Type != "uv" {
			t.Errorf("Type = %q, want %q", info.Type, "uv")
		}
	})

	t.Run("invalid runtime returns error", func(t *testing.T) {
		appConfig := &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.37.0",
			Runtime:     "nonexistent",
		}

		_, err := rm.GetUVCommandInfo("yamllint", appConfig, nil, nil)
		if err == nil {
			t.Error("expected error for nonexistent runtime, got nil")
		}
	})

	t.Run("deterministic paths", func(t *testing.T) {
		appConfig := &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.37.0",
			Runtime:     "uv",
		}

		info1, err := rm.GetUVCommandInfo("yamllint", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}

		info2, err := rm.GetUVCommandInfo("yamllint", appConfig, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}

		if info1.Command != info2.Command {
			t.Errorf("paths not deterministic: %q != %q", info1.Command, info2.Command)
		}
	})

	t.Run("different versions produce different paths", func(t *testing.T) {
		config1 := &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.37.0",
			Runtime:     "uv",
		}
		config2 := &binmanager.AppConfigUV{
			PackageName: "yamllint",
			Version:     "1.38.0",
			Runtime:     "uv",
		}

		info1, err := rm.GetUVCommandInfo("yamllint", config1, nil, nil)
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		info2, err := rm.GetUVCommandInfo("yamllint", config2, nil, nil)
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}

		if info1.Command == info2.Command {
			t.Error("different versions should produce different paths")
		}
	})
}

func TestGetUVCommandInfo_LockFileAffectsPath(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	configNoLock := &binmanager.AppConfigUV{
		PackageName: "yamllint",
		Version:     "1.37.0",
		Runtime:     "uv",
	}
	configWithLock := &binmanager.AppConfigUV{
		PackageName: "yamllint",
		Version:     "1.37.0",
		Runtime:     "uv",
		LockFile:    "version = 1\nrequires-python = \">=3.8\"",
	}

	infoNoLock, err := rm.GetUVCommandInfo("yamllint", configNoLock, nil, nil)
	if err != nil {
		t.Fatalf("GetUVCommandInfo() without lock error = %v", err)
	}
	infoWithLock, err := rm.GetUVCommandInfo("yamllint", configWithLock, nil, nil)
	if err != nil {
		t.Fatalf("GetUVCommandInfo() with lock error = %v", err)
	}

	if infoNoLock.Command == infoWithLock.Command {
		t.Error("lockFile should produce a different cache path")
	}
}

func TestGetUVCommandInfo_DifferentLockFilesProduceDifferentPaths(t *testing.T) {
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	config1 := &binmanager.AppConfigUV{
		PackageName: "yamllint",
		Version:     "1.37.0",
		Runtime:     "uv",
		LockFile:    "lockfile-content-v1",
	}
	config2 := &binmanager.AppConfigUV{
		PackageName: "yamllint",
		Version:     "1.37.0",
		Runtime:     "uv",
		LockFile:    "lockfile-content-v2",
	}

	info1, err := rm.GetUVCommandInfo("yamllint", config1, nil, nil)
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}
	info2, err := rm.GetUVCommandInfo("yamllint", config2, nil, nil)
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}

	if info1.Command == info2.Command {
		t.Error("different lockFile contents should produce different paths")
	}
}

func TestInstallUVAppAlreadyInstalled(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	appConfig := &binmanager.AppConfigUV{
		PackageName: "yamllint",
		Version:     "1.37.0",
		Runtime:     "uv",
	}

	appEnvPath, err := rm.GetAppPath("yamllint", config.RuntimeKindUV, uvVersionForHash("1.37.0", ""), nil, "", nil, nil, "uv")
	if err != nil {
		t.Fatalf("GetAppPath() error = %v", err)
	}

	binPath := getUVBinaryPath(appEnvPath, "yamllint")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho ok"), 0o755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}
	// A resolvable interpreter makes the venv healthy so the install gate skips.
	if err := os.WriteFile(getUVInterpreterPath(appEnvPath), []byte("python"), 0o755); err != nil {
		t.Fatalf("failed to write fake interpreter: %v", err)
	}
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	err = rm.InstallUVApp(context.Background(), "yamllint", appConfig, nil, nil, nil)
	if err != nil {
		t.Errorf("InstallUVApp() error = %v, expected nil for already installed app", err)
	}
}

func TestUVVenvHealthy(t *testing.T) {
	t.Run("healthy: binary and interpreter both resolve", func(t *testing.T) {
		dir := t.TempDir()
		binPath := getUVBinaryPath(dir, "yamllint")
		if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(getUVInterpreterPath(dir), []byte("python"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !uvVenvHealthy(dir, binPath) {
			t.Error("expected healthy venv to be reported as healthy")
		}
	})

	t.Run("dangling interpreter symlink is unhealthy", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink semantics differ on windows")
		}
		dir := t.TempDir()
		binPath := getUVBinaryPath(dir, "yamllint")
		if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Symlink pointing at a non-existent interpreter (cache restored onto fresh runner).
		if err := os.Symlink(filepath.Join(dir, "does", "not", "exist", "python"), getUVInterpreterPath(dir)); err != nil {
			t.Fatal(err)
		}
		if uvVenvHealthy(dir, binPath) {
			t.Error("expected dangling interpreter to be reported as unhealthy")
		}
	})

	t.Run("missing binary is unhealthy even if interpreter resolves", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".venv", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(getUVInterpreterPath(dir), []byte("python"), 0o755); err != nil {
			t.Fatal(err)
		}
		binPath := getUVBinaryPath(dir, "yamllint")
		if uvVenvHealthy(dir, binPath) {
			t.Error("expected missing binary to be reported as unhealthy")
		}
	})
}

func TestInstallUVAppHealthyVenvSkips(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	appConfig := &binmanager.AppConfigUV{
		PackageName: "yamllint",
		Version:     "1.37.0",
		Runtime:     "uv",
	}

	appEnvPath, err := rm.GetAppPath("yamllint", config.RuntimeKindUV, uvVersionForHash("1.37.0", ""), nil, "", nil, nil, "uv")
	if err != nil {
		t.Fatalf("GetAppPath() error = %v", err)
	}
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	binPath := getUVBinaryPath(appEnvPath, "yamllint")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho ok"), 0o755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}
	// A resolvable interpreter → healthy venv → install must be a no-op (no uv invocation).
	if err := os.WriteFile(getUVInterpreterPath(appEnvPath), []byte("python"), 0o755); err != nil {
		t.Fatalf("failed to write fake interpreter: %v", err)
	}

	if err := rm.InstallUVApp(context.Background(), "yamllint", appConfig, nil, nil, nil); err != nil {
		t.Errorf("InstallUVApp() error = %v, expected nil for healthy venv", err)
	}

	// The fake binary must be untouched (no rebuild attempted).
	if _, err := os.Stat(binPath); err != nil {
		t.Errorf("expected binary to remain after skip, stat error = %v", err)
	}
}

func TestInstallUVAppDanglingVenvRebuilds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	runtimes := makeTestRuntimes()
	rm := New(runtimes)

	appConfig := &binmanager.AppConfigUV{
		PackageName: "yamllint",
		Version:     "1.37.0",
		Runtime:     "uv",
	}

	appEnvPath, err := rm.GetAppPath("yamllint", config.RuntimeKindUV, uvVersionForHash("1.37.0", ""), nil, "", nil, nil, "uv")
	if err != nil {
		t.Fatalf("GetAppPath() error = %v", err)
	}
	defer func() { _ = os.RemoveAll(appEnvPath) }()

	binPath := getUVBinaryPath(appEnvPath, "yamllint")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho ok"), 0o755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}
	// Dangling interpreter symlink: the wrapper exists but the venv is broken.
	if err := os.Symlink(filepath.Join(appEnvPath, "missing", "python"), getUVInterpreterPath(appEnvPath)); err != nil {
		t.Fatalf("failed to create dangling symlink: %v", err)
	}

	// The dangling venv must NOT be trusted: install attempts a rebuild, which
	// fails here because the test uv runtime is not actually downloaded. The key
	// assertion is that it did not return nil (i.e. it did not treat the broken
	// venv as installed).
	err = rm.InstallUVApp(context.Background(), "yamllint", appConfig, nil, nil, nil)
	if err == nil {
		t.Error("expected dangling venv to trigger a rebuild attempt, got nil (treated as installed)")
	}

	// The stale app directory must have been removed during the rebuild attempt.
	if _, statErr := os.Stat(appEnvPath); !os.IsNotExist(statErr) {
		t.Errorf("expected stale app dir to be removed, stat error = %v", statErr)
	}
}

func TestResolveRequiresPython(t *testing.T) {
	t.Run("app-level value is used when set", func(t *testing.T) {
		got := resolveRequiresPython(">=3.10")
		if got != ">=3.10" {
			t.Errorf("got %q, want %q", got, ">=3.10")
		}
	})

	t.Run("falls back to >=3.12 when empty", func(t *testing.T) {
		got := resolveRequiresPython("")
		if got != ">=3.12" {
			t.Errorf("got %q, want %q", got, ">=3.12")
		}
	})

	t.Run("preserves exact constraint", func(t *testing.T) {
		got := resolveRequiresPython("==3.12.0")
		if got != "==3.12.0" {
			t.Errorf("got %q, want %q", got, "==3.12.0")
		}
	})

	t.Run("preserves complex constraint", func(t *testing.T) {
		got := resolveRequiresPython(">=3.8,<4")
		if got != ">=3.8,<4" {
			t.Errorf("got %q, want %q", got, ">=3.8,<4")
		}
	})
}

func TestUVVersionForHash(t *testing.T) {
	t.Run("includes requiresPython in hash key", func(t *testing.T) {
		v1 := uvVersionForHash("1.0.0", ">=3.10")
		v2 := uvVersionForHash("1.0.0", ">=3.12")
		if v1 == v2 {
			t.Error("different requiresPython values should produce different hash keys")
		}
	})

	t.Run("empty requiresPython uses default", func(t *testing.T) {
		v1 := uvVersionForHash("1.0.0", "")
		v2 := uvVersionForHash("1.0.0", ">=3.12")
		if v1 != v2 {
			t.Errorf("empty requiresPython should match default: got %q vs %q", v1, v2)
		}
	})
}

func TestBuildUVInstallArgs(t *testing.T) {
	t.Run("no lockfile: no --locked and no --no-build", func(t *testing.T) {
		args := buildUVInstallArgs("", nil)
		want := []string{"sync", "--no-install-project"}
		if !equalStringSlices(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
	})

	t.Run("with lockfile: adds --locked and --no-build", func(t *testing.T) {
		args := buildUVInstallArgs("version = 1\n", nil)
		want := []string{"sync", "--no-install-project", "--locked", "--no-build"}
		if !equalStringSlices(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
	})

	t.Run("lockfile + python version: appends --python", func(t *testing.T) {
		args := buildUVInstallArgs("version = 1\n", &config.RuntimeConfigUV{PythonVersion: "3.12.1"})
		want := []string{"sync", "--no-install-project", "--locked", "--no-build", "--python", "3.12.1"}
		if !equalStringSlices(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
	})

	t.Run("no lockfile + python version: --python without --no-build", func(t *testing.T) {
		args := buildUVInstallArgs("", &config.RuntimeConfigUV{PythonVersion: "3.12.1"})
		want := []string{"sync", "--no-install-project", "--python", "3.12.1"}
		if !equalStringSlices(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
	})

	t.Run("empty python version: no --python flag", func(t *testing.T) {
		args := buildUVInstallArgs("version = 1\n", &config.RuntimeConfigUV{PythonVersion: ""})
		want := []string{"sync", "--no-install-project", "--locked", "--no-build"}
		if !equalStringSlices(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
	})

	t.Run("no lockfile: --no-build must NOT be present", func(t *testing.T) {
		args := buildUVInstallArgs("", nil)
		for _, a := range args {
			if a == "--no-build" {
				t.Error("--no-build should not be present without a lockfile (would block all sdist builds without security justification)")
			}
		}
	})

	t.Run("with lockfile: --no-build must be present", func(t *testing.T) {
		args := buildUVInstallArgs("version = 1\n", nil)
		found := slices.Contains(args, "--no-build")
		if !found {
			t.Error("--no-build must be present when a lockfile is supplied (supply chain hardening)")
		}
	})
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuildPyprojectTOML(t *testing.T) {
	t.Run("with version", func(t *testing.T) {
		result := buildPyprojectTOML("yamllint", "yamllint", "1.38.0", ">=3.12")
		if !strings.Contains(result, `name = "datamitsu-yamllint"`) {
			t.Error("missing project name")
		}
		if !strings.Contains(result, `"yamllint==1.38.0"`) {
			t.Error("missing versioned dependency")
		}
		if !strings.Contains(result, `requires-python = ">=3.12"`) {
			t.Error("missing requires-python")
		}
	})

	t.Run("without version", func(t *testing.T) {
		result := buildPyprojectTOML("ruff", "ruff", "", ">=3.12")
		if !strings.Contains(result, `"ruff"`) {
			t.Error("missing unversioned dependency")
		}
		if strings.Contains(result, "==") {
			t.Error("should not have version constraint")
		}
	})

	t.Run("scoped package name", func(t *testing.T) {
		result := buildPyprojectTOML("myapp", "@scope/pkg", "1.0.0", ">=3.12")
		if !strings.Contains(result, `name = "datamitsu-scope-pkg"`) {
			t.Errorf("@ should be removed, / replaced with -: got %s", result)
		}
		if !strings.Contains(result, `"@scope/pkg==1.0.0"`) {
			t.Error("dependency should keep original package name")
		}
	})

	t.Run("newlines in values are escaped", func(t *testing.T) {
		result := buildPyprojectTOML("test", "pkg\ninjected", "1.0\r\nevil", ">=3.12")
		if strings.Contains(result, "\ninjected") || strings.Contains(result, "\r\nevil") {
			t.Errorf("newlines should be escaped in TOML output: got %s", result)
		}
	})

	t.Run("custom requires-python", func(t *testing.T) {
		result := buildPyprojectTOML("yamllint", "yamllint", "1.38.0", ">=3.10")
		if !strings.Contains(result, `requires-python = ">=3.10"`) {
			t.Errorf("expected requires-python = \">=3.10\", got %s", result)
		}
	})
}
