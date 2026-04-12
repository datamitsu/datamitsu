package env

import (
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestGetCachePath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	t.Run("uses custom cache dir with /cache suffix", func(t *testing.T) {
		customPath := "/custom/cache/path"
		if err := os.Setenv(cacheDir.Name, customPath); err != nil {
			t.Fatalf("failed to set env: %v", err)
		}

		got := GetCachePath()
		want := filepath.Join(customPath, "cache")
		if got != want {
			t.Errorf("GetCachePath() = %q, want %q", got, want)
		}
	})

	t.Run("XDG_CACHE_HOME with /cache suffix", func(t *testing.T) {
		_ = os.Unsetenv(cacheDir.Name)
		_ = os.Setenv("XDG_CACHE_HOME", "/xdg/cache")

		got := GetCachePath()
		want := filepath.Join("/xdg/cache", ldflags.PackageName, "cache")
		if got != want {
			t.Errorf("GetCachePath() = %q, want %q", got, want)
		}

		_ = os.Unsetenv("XDG_CACHE_HOME")
	})

	t.Run("uses default home dir path with /cache suffix", func(t *testing.T) {
		_ = os.Unsetenv(cacheDir.Name)
		_ = os.Unsetenv("XDG_CACHE_HOME")
		_ = os.Unsetenv("LOCALAPPDATA")

		got := GetCachePath()
		if got == "" {
			t.Error("GetCachePath() returned empty string")
		}

		if !filepath.IsAbs(got) {
			t.Errorf("GetCachePath() returned non-absolute path: %q", got)
		}

		if filepath.Base(got) != "cache" {
			t.Errorf("GetCachePath() should end with 'cache', got %q", got)
		}
	})
}

func TestGetStorePath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	t.Run("uses custom cache dir with /store suffix", func(t *testing.T) {
		customPath := "/custom/cache/path"
		if err := os.Setenv(cacheDir.Name, customPath); err != nil {
			t.Fatalf("failed to set env: %v", err)
		}

		got := GetStorePath()
		want := filepath.Join(customPath, "store")
		if got != want {
			t.Errorf("GetStorePath() = %q, want %q", got, want)
		}
	})

	t.Run("XDG_CACHE_HOME with /store suffix", func(t *testing.T) {
		_ = os.Unsetenv(cacheDir.Name)
		_ = os.Setenv("XDG_CACHE_HOME", "/xdg/cache")

		got := GetStorePath()
		want := filepath.Join("/xdg/cache", ldflags.PackageName, "store")
		if got != want {
			t.Errorf("GetStorePath() = %q, want %q", got, want)
		}

		_ = os.Unsetenv("XDG_CACHE_HOME")
	})

	t.Run("fallback to home directory with /store suffix", func(t *testing.T) {
		_ = os.Unsetenv(cacheDir.Name)
		_ = os.Unsetenv("XDG_CACHE_HOME")
		_ = os.Unsetenv("LOCALAPPDATA")

		got := GetStorePath()
		if got == "" {
			t.Error("GetStorePath() returned empty string")
		}

		if !filepath.IsAbs(got) {
			t.Errorf("GetStorePath() returned non-absolute path: %q", got)
		}

		if filepath.Base(got) != "store" {
			t.Errorf("GetStorePath() should end with 'store', got %q", got)
		}
	})

	t.Run("GetStorePath and GetCachePath return different paths", func(t *testing.T) {
		if err := os.Setenv(cacheDir.Name, "/tmp/test-datamitsu"); err != nil {
			t.Fatalf("failed to set env: %v", err)
		}

		cachePath := GetCachePath()
		storePath := GetStorePath()

		if cachePath == storePath {
			t.Errorf("GetCachePath() and GetStorePath() should differ, both returned %q", cachePath)
		}

		if cachePath != "/tmp/test-datamitsu/cache" {
			t.Errorf("GetCachePath() = %q, want /tmp/test-datamitsu/cache", cachePath)
		}
		if storePath != "/tmp/test-datamitsu/store" {
			t.Errorf("GetStorePath() = %q, want /tmp/test-datamitsu/store", storePath)
		}
	})
}

func TestGetBinPath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	if err := os.Setenv(cacheDir.Name, "/tmp/test-cache"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	binPath := GetBinPath()
	storePath := GetStorePath()

	expectedBinPath := filepath.Join(storePath, ".bin")
	if binPath != expectedBinPath {
		t.Errorf("GetBinPath() = %q, want %q", binPath, expectedBinPath)
	}

	if !filepath.IsAbs(binPath) {
		t.Errorf("GetBinPath() returned non-absolute path: %q", binPath)
	}

	want := filepath.Join("/tmp/test-cache", "store", ".bin")
	if binPath != want {
		t.Errorf("GetBinPath() = %q, want %q", binPath, want)
	}
}

func TestGetLogLevel(t *testing.T) {
	originalEnv := os.Getenv(logLevel.Name)
	defer func() {
		if err := os.Setenv(logLevel.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	tests := []struct {
		name     string
		envValue string
		want     zapcore.Level
	}{
		{
			name:     "debug level",
			envValue: "debug",
			want:     zapcore.DebugLevel,
		},
		{
			name:     "info level",
			envValue: "info",
			want:     zapcore.InfoLevel,
		},
		{
			name:     "warn level",
			envValue: "warn",
			want:     zapcore.WarnLevel,
		},
		{
			name:     "error level",
			envValue: "error",
			want:     zapcore.ErrorLevel,
		},
		{
			name:     "invalid level defaults to info",
			envValue: "invalid",
			want:     zapcore.InfoLevel,
		},
		{
			name:     "empty env defaults to info",
			envValue: "",
			want:     zapcore.InfoLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				if err := os.Setenv(logLevel.Name, tt.envValue); err != nil {
					t.Fatalf("failed to set env: %v", err)
				}
			} else {
				_ = os.Unsetenv(logLevel.Name)
			}

			got := GetLogLevel()
			if got != tt.want {
				t.Errorf("GetLogLevel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDefaultMaxWorkers(t *testing.T) {
	result := getDefaultMaxWorkers()
	n, err := strconv.Atoi(result)
	if err != nil {
		t.Fatalf("getDefaultMaxWorkers() returned non-integer: %q", result)
	}
	if n < 4 {
		t.Errorf("getDefaultMaxWorkers() = %d, want >= 4", n)
	}
	if n > 16 {
		t.Errorf("getDefaultMaxWorkers() = %d, want <= 16", n)
	}

	expected := runtime.NumCPU() * 3 / 4
	if expected < 4 {
		expected = 4
	}
	if expected > 16 {
		expected = 16
	}
	if n != expected {
		t.Errorf("getDefaultMaxWorkers() = %d, want %d for NumCPU=%d", n, expected, runtime.NumCPU())
	}
}

func TestGetRuntimesPath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	t.Run("returns path under store dir", func(t *testing.T) {
		if err := os.Setenv(cacheDir.Name, "/tmp/test-cache"); err != nil {
			t.Fatalf("failed to set env: %v", err)
		}

		got := GetRuntimesPath()
		want := filepath.Join("/tmp/test-cache", "store", ".runtimes")
		if got != want {
			t.Errorf("GetRuntimesPath() = %q, want %q", got, want)
		}
	})
}

func TestGetRuntimeBinaryPath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	if err := os.Setenv(cacheDir.Name, "/tmp/test-cache"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	got := GetRuntimeBinaryPath("uv", "abc123")
	want := filepath.Join("/tmp/test-cache", "store", ".runtimes", "uv", "abc123")
	if got != want {
		t.Errorf("GetRuntimeBinaryPath() = %q, want %q", got, want)
	}
}

func TestGetAppsPath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	if err := os.Setenv(cacheDir.Name, "/tmp/test-cache"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	got := GetAppsPath()
	want := filepath.Join("/tmp/test-cache", "store", ".apps")
	if got != want {
		t.Errorf("GetAppsPath() = %q, want %q", got, want)
	}
}

func TestGetAppEnvPath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	if err := os.Setenv(cacheDir.Name, "/tmp/test-cache"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	got := GetAppEnvPath("fnm", "eslint", "def456")
	want := filepath.Join("/tmp/test-cache", "store", ".apps", "fnm", "eslint", "def456")
	if got != want {
		t.Errorf("GetAppEnvPath() = %q, want %q", got, want)
	}
}

func TestGetPNPMStorePath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	if err := os.Setenv(cacheDir.Name, "/tmp/test-cache"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	got := GetPNPMStorePath()
	want := filepath.Join("/tmp/test-cache", "store", ".pnpm-store")
	if got != want {
		t.Errorf("GetPNPMStorePath() = %q, want %q", got, want)
	}
}

func TestGetNodeBinaryPath(t *testing.T) {
	tests := []struct {
		name        string
		storeRoot   string
		nodeVersion string
		wantUnix    string
		wantWindows string
	}{
		{
			name:        "standard version",
			storeRoot:   "/tmp/test-cache",
			nodeVersion: "20.11.1",
			wantUnix:    filepath.Join("/tmp/test-cache", ".runtimes", "fnm-nodes", "v20.11.1", "installation", "bin", "node"),
			wantWindows: filepath.Join("/tmp/test-cache", ".runtimes", "fnm-nodes", "v20.11.1", "installation", "node.exe"),
		},
		{
			name:        "different version",
			storeRoot:   "/home/user/.cache/datamitsu",
			nodeVersion: "22.0.0",
			wantUnix:    filepath.Join("/home/user/.cache/datamitsu", ".runtimes", "fnm-nodes", "v22.0.0", "installation", "bin", "node"),
			wantWindows: filepath.Join("/home/user/.cache/datamitsu", ".runtimes", "fnm-nodes", "v22.0.0", "installation", "node.exe"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetNodeBinaryPath(tt.storeRoot, tt.nodeVersion)
			want := tt.wantUnix
			if runtime.GOOS == "windows" {
				want = tt.wantWindows
			}
			if got != want {
				t.Errorf("GetNodeBinaryPath() = %q, want %q", got, want)
			}
		})
	}
}

func TestGetPNPMPath(t *testing.T) {
	tests := []struct {
		name        string
		storeRoot   string
		pnpmVersion string
		pnpmHash    string
		want        string
	}{
		{
			name:        "standard version",
			storeRoot:   "/tmp/test-cache",
			pnpmVersion: "9.15.4",
			pnpmHash:    "abc123",
			want:        filepath.Join("/tmp/test-cache", ".runtimes", "fnm-pnpm", "9.15.4", "abc123", "package", "bin", "pnpm.cjs"),
		},
		{
			name:        "different version",
			storeRoot:   "/home/user/.cache/datamitsu",
			pnpmVersion: "10.0.0",
			pnpmHash:    "def456",
			want:        filepath.Join("/home/user/.cache/datamitsu", ".runtimes", "fnm-pnpm", "10.0.0", "def456", "package", "bin", "pnpm.cjs"),
		},
		{
			name:        "different hash same version gets different path",
			storeRoot:   "/tmp/test-cache",
			pnpmVersion: "9.15.4",
			pnpmHash:    "different789",
			want:        filepath.Join("/tmp/test-cache", ".runtimes", "fnm-pnpm", "9.15.4", "different789", "package", "bin", "pnpm.cjs"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPNPMPath(tt.storeRoot, tt.pnpmVersion, tt.pnpmHash)
			if got != tt.want {
				t.Errorf("GetPNPMPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetProjectCachePath(t *testing.T) {
	originalEnv := os.Getenv(cacheDir.Name)
	defer func() {
		if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
			t.Errorf("failed to restore env: %v", err)
		}
	}()

	if err := os.Setenv(cacheDir.Name, "/tmp/test-cache"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	t.Run("basic case with project and tool", func(t *testing.T) {
		got, err := GetProjectCachePath("/home/user/myproject", "packages/frontend", "tsc")
		if err != nil {
			t.Fatalf("GetProjectCachePath() error = %v", err)
		}

		if !filepath.IsAbs(got) {
			t.Errorf("returned non-absolute path: %q", got)
		}

		wantSuffix := filepath.Join("cache", "packages", "frontend", "tsc")
		if got[len(got)-len(wantSuffix):] != wantSuffix {
			t.Errorf("path should end with %q, got %q", wantSuffix, got)
		}
	})

	t.Run("root-level project with empty relativeProjectPath", func(t *testing.T) {
		got, err := GetProjectCachePath("/home/user/myproject", "", "golangci-lint")
		if err != nil {
			t.Fatalf("GetProjectCachePath() error = %v", err)
		}

		wantSuffix := filepath.Join("cache", "golangci-lint")
		if got[len(got)-len(wantSuffix):] != wantSuffix {
			t.Errorf("path should end with %q, got %q", wantSuffix, got)
		}
	})

	t.Run("nested project path", func(t *testing.T) {
		got, err := GetProjectCachePath("/home/user/myproject", "services/api/core", "eslint")
		if err != nil {
			t.Fatalf("GetProjectCachePath() error = %v", err)
		}

		wantSuffix := filepath.Join("cache", "services", "api", "core", "eslint")
		if got[len(got)-len(wantSuffix):] != wantSuffix {
			t.Errorf("path should end with %q, got %q", wantSuffix, got)
		}
	})

	t.Run("path cleaning removes extra slashes", func(t *testing.T) {
		got, err := GetProjectCachePath("/home/user/myproject", "packages//frontend/", "tsc")
		if err != nil {
			t.Fatalf("GetProjectCachePath() error = %v", err)
		}

		cleanSuffix := filepath.Join("cache", "packages", "frontend", "tsc")
		if got[len(got)-len(cleanSuffix):] != cleanSuffix {
			t.Errorf("path should be cleaned to end with %q, got %q", cleanSuffix, got)
		}
	})

	t.Run("path cleaning handles dot segments", func(t *testing.T) {
		got, err := GetProjectCachePath("/home/user/myproject", "./packages/../packages/frontend", "tsc")
		if err != nil {
			t.Fatalf("GetProjectCachePath() error = %v", err)
		}

		cleanSuffix := filepath.Join("cache", "packages", "frontend", "tsc")
		if got[len(got)-len(cleanSuffix):] != cleanSuffix {
			t.Errorf("path should be cleaned to end with %q, got %q", cleanSuffix, got)
		}
	})

	t.Run("same input produces same hash", func(t *testing.T) {
		path1, err := GetProjectCachePath("/home/user/myproject", "pkg", "tsc")
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		path2, err := GetProjectCachePath("/home/user/myproject", "pkg", "tsc")
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if path1 != path2 {
			t.Errorf("same input produced different paths: %q vs %q", path1, path2)
		}
	})

	t.Run("different git roots produce different hashes", func(t *testing.T) {
		path1, err := GetProjectCachePath("/home/user/project-a", "pkg", "tsc")
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		path2, err := GetProjectCachePath("/home/user/project-b", "pkg", "tsc")
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if path1 == path2 {
			t.Errorf("different git roots produced same path: %q", path1)
		}
	})

	t.Run("different tools produce different paths", func(t *testing.T) {
		path1, err := GetProjectCachePath("/home/user/myproject", "pkg", "tsc")
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		path2, err := GetProjectCachePath("/home/user/myproject", "pkg", "eslint")
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if path1 == path2 {
			t.Errorf("different tools produced same path: %q", path1)
		}
	})

	t.Run("different projects produce different paths", func(t *testing.T) {
		path1, err := GetProjectCachePath("/home/user/myproject", "packages/frontend", "tsc")
		if err != nil {
			t.Fatalf("first call error = %v", err)
		}
		path2, err := GetProjectCachePath("/home/user/myproject", "packages/backend", "tsc")
		if err != nil {
			t.Fatalf("second call error = %v", err)
		}
		if path1 == path2 {
			t.Errorf("different projects produced same path: %q", path1)
		}
	})

	t.Run("rejects absolute relativeProjectPath", func(t *testing.T) {
		cases := []string{
			"/etc/passwd",
			"/tmp/evil",
			"/home/user/project",
		}
		for _, c := range cases {
			_, err := GetProjectCachePath("/home/user/myproject", c, "tsc")
			if err == nil {
				t.Errorf("GetProjectCachePath(%q) should return error for absolute path", c)
			}
		}
	})

	t.Run("rejects path traversal via relativeProjectPath", func(t *testing.T) {
		cases := []string{
			"../../etc",
			"../..",
			"..",
			"foo/../../bar",
		}
		for _, c := range cases {
			_, err := GetProjectCachePath("/home/user/myproject", c, "tsc")
			if err == nil {
				t.Errorf("GetProjectCachePath(%q) should return error for path traversal", c)
			}
		}
	})

	t.Run("rejects invalid tool names", func(t *testing.T) {
		cases := []string{
			"tool/escape",
			"tool\\escape",
			"tool..name",
			"../escape",
		}
		for _, c := range cases {
			_, err := GetProjectCachePath("/home/user/myproject", "pkg", c)
			if err == nil {
				t.Errorf("GetProjectCachePath(toolName=%q) should return error", c)
			}
		}
	})

	t.Run("uses cache path not store path", func(t *testing.T) {
		got, err := GetProjectCachePath("/home/user/myproject", "", "tsc")
		if err != nil {
			t.Fatalf("GetProjectCachePath() error = %v", err)
		}

		cachePath := GetCachePath()
		storePath := GetStorePath()

		if len(got) < len(cachePath) || got[:len(cachePath)] != cachePath {
			t.Errorf("GetProjectCachePath() = %q, should be under cache path %q", got, cachePath)
		}

		if len(got) >= len(storePath) && got[:len(storePath)] == storePath {
			t.Errorf("GetProjectCachePath() = %q, should NOT be under store path %q", got, storePath)
		}
	})

	t.Run("hash is 32-char hex (XXH3-128)", func(t *testing.T) {
		got, err := GetProjectCachePath("/some/path", "", "tool")
		if err != nil {
			t.Fatalf("GetProjectCachePath() error = %v", err)
		}
		// Path structure: /tmp/test-cache/projects/{hash}/cache/tool
		// Navigate up past tool and cache to get hash
		cacheDir := filepath.Dir(filepath.Dir(got))
		hashDir := filepath.Base(cacheDir)
		if len(hashDir) != 32 {
			t.Errorf("hash component length = %d, want 32, got %q", len(hashDir), hashDir)
		}
	})
}

func TestGetMaxParallelWorkers(t *testing.T) {
	originalEnv := os.Getenv(maxParallelWorkers.Name)
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv(maxParallelWorkers.Name, originalEnv)
		} else {
			_ = os.Unsetenv(maxParallelWorkers.Name)
		}
	}()

	t.Run("dynamic default without env var", func(t *testing.T) {
		_ = os.Unsetenv(maxParallelWorkers.Name)
		got := GetMaxParallelWorkers()
		if got < 4 || got > 16 {
			t.Errorf("GetMaxParallelWorkers() = %d, want between 4 and 16", got)
		}
	})

	t.Run("env var override", func(t *testing.T) {
		_ = os.Setenv(maxParallelWorkers.Name, "8")
		got := GetMaxParallelWorkers()
		if got != 8 {
			t.Errorf("GetMaxParallelWorkers() = %d, want 8", got)
		}
	})

	t.Run("parse error fallback to dynamic default", func(t *testing.T) {
		_ = os.Setenv(maxParallelWorkers.Name, "notanumber")
		got := GetMaxParallelWorkers()
		dynamicDefault, _ := strconv.Atoi(getDefaultMaxWorkers())
		if got != dynamicDefault {
			t.Errorf("GetMaxParallelWorkers() with invalid env = %d, want dynamic default %d", got, dynamicDefault)
		}
	})

	t.Run("zero value fallback to dynamic default", func(t *testing.T) {
		_ = os.Setenv(maxParallelWorkers.Name, "0")
		got := GetMaxParallelWorkers()
		dynamicDefault, _ := strconv.Atoi(getDefaultMaxWorkers())
		if got != dynamicDefault {
			t.Errorf("GetMaxParallelWorkers() with zero = %d, want dynamic default %d", got, dynamicDefault)
		}
	})
}

func TestNoSponsor(t *testing.T) {
	originalEnv := os.Getenv(noSponsor.Name)
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv(noSponsor.Name, originalEnv)
		} else {
			_ = os.Unsetenv(noSponsor.Name)
		}
	}()

	t.Run("returns false when unset", func(t *testing.T) {
		_ = os.Unsetenv(noSponsor.Name)
		if NoSponsor() {
			t.Error("NoSponsor() = true, want false when env var is unset")
		}
	})

	t.Run("returns true when set to 1", func(t *testing.T) {
		_ = os.Setenv(noSponsor.Name, "1")
		if !NoSponsor() {
			t.Error("NoSponsor() = false, want true when env var is '1'")
		}
	})

	t.Run("returns true when set to true", func(t *testing.T) {
		_ = os.Setenv(noSponsor.Name, "true")
		if !NoSponsor() {
			t.Error("NoSponsor() = false, want true when env var is 'true'")
		}
	})

	t.Run("returns false when set to empty string", func(t *testing.T) {
		_ = os.Setenv(noSponsor.Name, "")
		if NoSponsor() {
			t.Error("NoSponsor() = true, want false when env var is empty string")
		}
	})
}

func TestIsCI(t *testing.T) {
	originalEnv := os.Getenv("CI")
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv("CI", originalEnv)
		} else {
			_ = os.Unsetenv("CI")
		}
	}()

	t.Run("returns false when unset", func(t *testing.T) {
		_ = os.Unsetenv("CI")
		if IsCI() {
			t.Error("IsCI() = true, want false when CI is unset")
		}
	})

	t.Run("returns true when set to true", func(t *testing.T) {
		_ = os.Setenv("CI", "true")
		if !IsCI() {
			t.Error("IsCI() = false, want true when CI is 'true'")
		}
	})

	t.Run("returns true when set to 1", func(t *testing.T) {
		_ = os.Setenv("CI", "1")
		if !IsCI() {
			t.Error("IsCI() = false, want true when CI is '1'")
		}
	})

	t.Run("returns true when set to yes", func(t *testing.T) {
		_ = os.Setenv("CI", "yes")
		if !IsCI() {
			t.Error("IsCI() = false, want true when CI is 'yes'")
		}
	})

	t.Run("returns false when set to empty string", func(t *testing.T) {
		_ = os.Setenv("CI", "")
		if IsCI() {
			t.Error("IsCI() = true, want false when CI is empty string")
		}
	})
}

func TestGetBinaryCommandOverride(t *testing.T) {
	originalEnv := os.Getenv(binaryCommandOverride.Name)
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv(binaryCommandOverride.Name, originalEnv)
		} else {
			_ = os.Unsetenv(binaryCommandOverride.Name)
		}
	}()

	t.Run("returns empty string when unset", func(t *testing.T) {
		_ = os.Unsetenv(binaryCommandOverride.Name)
		got := GetBinaryCommandOverride()
		if got != "" {
			t.Errorf("GetBinaryCommandOverride() = %q, want empty string", got)
		}
	})

	t.Run("returns custom path when set", func(t *testing.T) {
		_ = os.Setenv(binaryCommandOverride.Name, "/custom/path")
		got := GetBinaryCommandOverride()
		if got != "/custom/path" {
			t.Errorf("GetBinaryCommandOverride() = %q, want '/custom/path'", got)
		}
	})
}
