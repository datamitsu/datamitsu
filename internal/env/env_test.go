package env

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/ldflags"

	"go.uber.org/zap/zapcore"
)

func TestGetCachePath(t *testing.T) {
	// t.Setenv registers cleanup that restores cacheDir.Name even though
	// subtests below os.Unsetenv it mid-test.
	t.Setenv(cacheDir.Name, os.Getenv(cacheDir.Name))

	t.Run("uses custom cache dir with /cache suffix", func(t *testing.T) {
		customPath := "/custom/cache/path"
		t.Setenv(cacheDir.Name, customPath)

		got := GetCachePath()
		want := filepath.Join(customPath, "cache")
		if got != want {
			t.Errorf("GetCachePath() = %q, want %q", got, want)
		}
	})

	t.Run("XDG_CACHE_HOME with /cache suffix", func(t *testing.T) {
		_ = os.Unsetenv(cacheDir.Name)
		t.Setenv("XDG_CACHE_HOME", "/xdg/cache")

		got := GetCachePath()
		want := filepath.Join("/xdg/cache", ldflags.PackageName, "cache")
		if got != want {
			t.Errorf("GetCachePath() = %q, want %q", got, want)
		}
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
	// t.Setenv registers cleanup that restores cacheDir.Name even though
	// subtests below os.Unsetenv it mid-test.
	t.Setenv(cacheDir.Name, os.Getenv(cacheDir.Name))

	t.Run("uses custom cache dir with /store suffix", func(t *testing.T) {
		customPath := "/custom/cache/path"
		t.Setenv(cacheDir.Name, customPath)

		got := GetStorePath()
		want := filepath.Join(customPath, "store")
		if got != want {
			t.Errorf("GetStorePath() = %q, want %q", got, want)
		}
	})

	t.Run("XDG_CACHE_HOME with /store suffix", func(t *testing.T) {
		_ = os.Unsetenv(cacheDir.Name)
		t.Setenv("XDG_CACHE_HOME", "/xdg/cache")

		got := GetStorePath()
		want := filepath.Join("/xdg/cache", ldflags.PackageName, "store")
		if got != want {
			t.Errorf("GetStorePath() = %q, want %q", got, want)
		}
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
		t.Setenv(cacheDir.Name, "/tmp/test-datamitsu")

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
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

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
	// t.Setenv registers cleanup that restores logLevel.Name even though
	// subtests below os.Unsetenv it mid-test.
	t.Setenv(logLevel.Name, os.Getenv(logLevel.Name))

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
			name:     "invalid level defaults to warn",
			envValue: "invalid",
			want:     zapcore.WarnLevel,
		},
		{
			name:     "empty env defaults to warn",
			envValue: "",
			want:     zapcore.WarnLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(logLevel.Name, tt.envValue)
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

func TestGetLogFormat(t *testing.T) {
	// t.Setenv registers cleanup that restores logFormat.Name even though
	// subtests below os.Unsetenv it mid-test.
	t.Setenv(logFormat.Name, os.Getenv(logFormat.Name))

	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{name: "default is console", envValue: "", want: "console"},
		{name: "jsonl", envValue: "jsonl", want: "jsonl"},
		{name: "console", envValue: "console", want: "console"},
		{name: "case-insensitive", envValue: "JSONL", want: "jsonl"},
		{name: "whitespace trimmed", envValue: "  jsonl  ", want: "jsonl"},
		{name: "unknown falls back to console", envValue: "yaml", want: "console"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(logFormat.Name, tt.envValue)
			} else {
				_ = os.Unsetenv(logFormat.Name)
			}

			if got := GetLogFormat(); got != tt.want {
				t.Errorf("GetLogFormat() = %q, want %q", got, tt.want)
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

	expected := min(max(runtime.NumCPU()*3/4, 4), 16)
	if n != expected {
		t.Errorf("getDefaultMaxWorkers() = %d, want %d for NumCPU=%d", n, expected, runtime.NumCPU())
	}
}

func TestGetRuntimesPath(t *testing.T) {
	t.Run("returns path under store dir", func(t *testing.T) {
		t.Setenv(cacheDir.Name, "/tmp/test-cache")

		got := GetRuntimesPath()
		want := filepath.Join("/tmp/test-cache", "store", ".runtimes")
		if got != want {
			t.Errorf("GetRuntimesPath() = %q, want %q", got, want)
		}
	})
}

func TestGetRuntimeBinaryPath(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

	got := GetRuntimeBinaryPath("uv", "abc123")
	want := filepath.Join("/tmp/test-cache", "store", ".runtimes", "uv", "abc123")
	if got != want {
		t.Errorf("GetRuntimeBinaryPath() = %q, want %q", got, want)
	}
}

func TestGetAppsPath(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

	got := GetAppsPath()
	want := filepath.Join("/tmp/test-cache", "store", ".apps")
	if got != want {
		t.Errorf("GetAppsPath() = %q, want %q", got, want)
	}
}

func TestGetAppEnvPath(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

	got := GetAppEnvPath("node", "eslint", "def456")
	want := filepath.Join("/tmp/test-cache", "store", ".apps", "node", "eslint", "def456")
	if got != want {
		t.Errorf("GetAppEnvPath() = %q, want %q", got, want)
	}
}

func TestGetPNPMStorePath(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

	got := GetPNPMStorePath()
	want := filepath.Join("/tmp/test-cache", "store", ".pnpm-store")
	if got != want {
		t.Errorf("GetPNPMStorePath() = %q, want %q", got, want)
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
			want:        filepath.Join("/tmp/test-cache", ".runtimes", "pnpm", "9.15.4", "abc123", "package", "bin", "pnpm.cjs"),
		},
		{
			name:        "different version",
			storeRoot:   "/home/user/.cache/datamitsu",
			pnpmVersion: "10.0.0",
			pnpmHash:    "def456",
			want:        filepath.Join("/home/user/.cache/datamitsu", ".runtimes", "pnpm", "10.0.0", "def456", "package", "bin", "pnpm.cjs"),
		},
		{
			name:        "different hash same version gets different path",
			storeRoot:   "/tmp/test-cache",
			pnpmVersion: "9.15.4",
			pnpmHash:    "different789",
			want:        filepath.Join("/tmp/test-cache", ".runtimes", "pnpm", "9.15.4", "different789", "package", "bin", "pnpm.cjs"),
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
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

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
	// t.Setenv registers cleanup that restores maxParallelWorkers.Name even
	// though the subtest below os.Unsetenv it mid-test.
	t.Setenv(maxParallelWorkers.Name, os.Getenv(maxParallelWorkers.Name))

	t.Run("dynamic default without env var", func(t *testing.T) {
		_ = os.Unsetenv(maxParallelWorkers.Name)
		got := GetMaxParallelWorkers()
		if got < 4 || got > 16 {
			t.Errorf("GetMaxParallelWorkers() = %d, want between 4 and 16", got)
		}
	})

	t.Run("env var override", func(t *testing.T) {
		t.Setenv(maxParallelWorkers.Name, "8")
		got := GetMaxParallelWorkers()
		if got != 8 {
			t.Errorf("GetMaxParallelWorkers() = %d, want 8", got)
		}
	})

	t.Run("parse error fallback to dynamic default", func(t *testing.T) {
		t.Setenv(maxParallelWorkers.Name, "notanumber")
		got := GetMaxParallelWorkers()
		dynamicDefault, _ := strconv.Atoi(getDefaultMaxWorkers())
		if got != dynamicDefault {
			t.Errorf("GetMaxParallelWorkers() with invalid env = %d, want dynamic default %d", got, dynamicDefault)
		}
	})

	t.Run("zero value fallback to dynamic default", func(t *testing.T) {
		t.Setenv(maxParallelWorkers.Name, "0")
		got := GetMaxParallelWorkers()
		dynamicDefault, _ := strconv.Atoi(getDefaultMaxWorkers())
		if got != dynamicDefault {
			t.Errorf("GetMaxParallelWorkers() with zero = %d, want dynamic default %d", got, dynamicDefault)
		}
	})
}

func TestNoSponsor(t *testing.T) {
	// t.Setenv registers cleanup that restores noSponsor.Name even though
	// the subtest below os.Unsetenv it mid-test.
	t.Setenv(noSponsor.Name, os.Getenv(noSponsor.Name))

	t.Run("returns false when unset", func(t *testing.T) {
		_ = os.Unsetenv(noSponsor.Name)
		if NoSponsor() {
			t.Error("NoSponsor() = true, want false when env var is unset")
		}
	})

	t.Run("returns true when set to 1", func(t *testing.T) {
		t.Setenv(noSponsor.Name, "1")
		if !NoSponsor() {
			t.Error("NoSponsor() = false, want true when env var is '1'")
		}
	})

	t.Run("returns true when set to true", func(t *testing.T) {
		t.Setenv(noSponsor.Name, "true")
		if !NoSponsor() {
			t.Error("NoSponsor() = false, want true when env var is 'true'")
		}
	})

	t.Run("returns false when set to empty string", func(t *testing.T) {
		t.Setenv(noSponsor.Name, "")
		if NoSponsor() {
			t.Error("NoSponsor() = true, want false when env var is empty string")
		}
	})
}

func TestIsCI(t *testing.T) {
	// t.Setenv registers cleanup that restores CI even though the subtest
	// below os.Unsetenv it mid-test.
	t.Setenv("CI", os.Getenv("CI"))

	t.Run("returns false when unset", func(t *testing.T) {
		_ = os.Unsetenv("CI")
		if IsCI() {
			t.Error("IsCI() = true, want false when CI is unset")
		}
	})

	t.Run("returns true when set to true", func(t *testing.T) {
		t.Setenv("CI", "true")
		if !IsCI() {
			t.Error("IsCI() = false, want true when CI is 'true'")
		}
	})

	t.Run("returns true when set to 1", func(t *testing.T) {
		t.Setenv("CI", "1")
		if !IsCI() {
			t.Error("IsCI() = false, want true when CI is '1'")
		}
	})

	t.Run("returns true when set to yes", func(t *testing.T) {
		t.Setenv("CI", "yes")
		if !IsCI() {
			t.Error("IsCI() = false, want true when CI is 'yes'")
		}
	})

	t.Run("returns false when set to empty string", func(t *testing.T) {
		t.Setenv("CI", "")
		if IsCI() {
			t.Error("IsCI() = true, want false when CI is empty string")
		}
	})
}

func TestGetBinaryCommandOverride(t *testing.T) {
	// t.Setenv registers cleanup that restores binaryCommandOverride.Name even
	// though the subtest below os.Unsetenv it mid-test.
	t.Setenv(binaryCommandOverride.Name, os.Getenv(binaryCommandOverride.Name))

	t.Run("returns empty string when unset", func(t *testing.T) {
		_ = os.Unsetenv(binaryCommandOverride.Name)
		got := GetBinaryCommandOverride()
		if got != "" {
			t.Errorf("GetBinaryCommandOverride() = %q, want empty string", got)
		}
	})

	t.Run("returns custom path when set", func(t *testing.T) {
		t.Setenv(binaryCommandOverride.Name, "/custom/path")
		got := GetBinaryCommandOverride()
		if got != "/custom/path" {
			t.Errorf("GetBinaryCommandOverride() = %q, want '/custom/path'", got)
		}
	})
}

func TestGetOCIRegistry(t *testing.T) {
	// t.Setenv registers cleanup that restores ociRegistry.Name even though the
	// subtest below os.Unsetenv it mid-test.
	t.Setenv(ociRegistry.Name, os.Getenv(ociRegistry.Name))

	t.Run("returns ghcr.io default when unset", func(t *testing.T) {
		_ = os.Unsetenv(ociRegistry.Name)
		got := GetOCIRegistry()
		if got != "ghcr.io" {
			t.Errorf("GetOCIRegistry() = %q, want 'ghcr.io'", got)
		}
	})

	t.Run("returns override when set", func(t *testing.T) {
		t.Setenv(ociRegistry.Name, "registry.example.com")
		got := GetOCIRegistry()
		if got != "registry.example.com" {
			t.Errorf("GetOCIRegistry() = %q, want 'registry.example.com'", got)
		}
	})
}

func TestInstallTimeoutSeconds(t *testing.T) {
	// t.Setenv registers cleanup that restores installTimeout.Name even though
	// subtests below os.Unsetenv it mid-test.
	t.Setenv(installTimeout.Name, os.Getenv(installTimeout.Name))

	tests := []struct {
		name  string
		set   bool
		value string
		want  int
	}{
		{"default when unset", false, "", 600},
		{"override", true, "1200", 1200},
		{"zero is valid (disabled)", true, "0", 0},
		{"negative falls back", true, "-5", 600},
		{"invalid falls back", true, "abc", 600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(installTimeout.Name, tt.value)
			} else {
				_ = os.Unsetenv(installTimeout.Name)
			}
			if got := InstallTimeoutSeconds(); got != tt.want {
				t.Errorf("InstallTimeoutSeconds() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMinimumReleaseAgeMinutes(t *testing.T) {
	// t.Setenv registers cleanup that restores minimumReleaseAge.Name even
	// though subtests below os.Unsetenv it mid-test.
	t.Setenv(minimumReleaseAge.Name, os.Getenv(minimumReleaseAge.Name))

	tests := []struct {
		name  string
		set   bool
		value string
		want  int
	}{
		{"default when unset", false, "", 10080},
		{"override", true, "1440", 1440},
		{"zero is valid (disabled)", true, "0", 0},
		{"negative falls back", true, "-1", 10080},
		{"invalid falls back", true, "xyz", 10080},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(minimumReleaseAge.Name, tt.value)
			} else {
				_ = os.Unsetenv(minimumReleaseAge.Name)
			}
			if got := MinimumReleaseAgeMinutes(); got != tt.want {
				t.Errorf("MinimumReleaseAgeMinutes() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOffline(t *testing.T) {
	t.Setenv(offline.Name, os.Getenv(offline.Name))

	t.Run("false when unset", func(t *testing.T) {
		_ = os.Unsetenv(offline.Name)
		if Offline() {
			t.Error("Offline() = true, want false when unset")
		}
	})

	t.Run("true for any non-empty value", func(t *testing.T) {
		t.Setenv(offline.Name, "1")
		if !Offline() {
			t.Error("Offline() = false, want true when set")
		}
	})
}

func TestOfflineVarName(t *testing.T) {
	if got := OfflineVarName(); got != offline.Name {
		t.Errorf("OfflineVarName() = %q, want %q", got, offline.Name)
	}
}

func TestNoOCI(t *testing.T) {
	t.Setenv(noOCI.Name, os.Getenv(noOCI.Name))

	t.Run("false when unset", func(t *testing.T) {
		_ = os.Unsetenv(noOCI.Name)
		if NoOCI() {
			t.Error("NoOCI() = true, want false when unset")
		}
	})

	t.Run("true for any non-empty value", func(t *testing.T) {
		t.Setenv(noOCI.Name, "1")
		if !NoOCI() {
			t.Error("NoOCI() = false, want true when set")
		}
	})
}

func TestNoParse(t *testing.T) {
	t.Setenv(noParse.Name, os.Getenv(noParse.Name))

	t.Run("false when unset", func(t *testing.T) {
		_ = os.Unsetenv(noParse.Name)
		if NoParse() {
			t.Error("NoParse() = true, want false when unset")
		}
	})

	t.Run("true for any non-empty value", func(t *testing.T) {
		t.Setenv(noParse.Name, "1")
		if !NoParse() {
			t.Error("NoParse() = false, want true when set")
		}
	})
}

func TestGetMaxCommandLength(t *testing.T) {
	t.Setenv(maxCmdLength.Name, os.Getenv(maxCmdLength.Name))

	defaultValue, _ := strconv.Atoi(maxCmdLength.DefaultValue)

	tests := []struct {
		name  string
		set   bool
		value string
		want  int
	}{
		{"default when unset", false, "", defaultValue},
		{"override", true, "64000", 64000},
		{"zero falls back", true, "0", defaultValue},
		{"negative falls back", true, "-1", defaultValue},
		{"invalid falls back", true, "abc", defaultValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(maxCmdLength.Name, tt.value)
			} else {
				_ = os.Unsetenv(maxCmdLength.Name)
			}
			if got := GetMaxCommandLength(); got != tt.want {
				t.Errorf("GetMaxCommandLength() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetMaxErrorCommandDisplay(t *testing.T) {
	t.Setenv(maxErrorCommandDisplay.Name, os.Getenv(maxErrorCommandDisplay.Name))

	defaultValue, _ := strconv.Atoi(maxErrorCommandDisplay.DefaultValue)

	tests := []struct {
		name  string
		set   bool
		value string
		want  int
	}{
		{"default when unset", false, "", defaultValue},
		{"override", true, "200", 200},
		{"zero falls back", true, "0", defaultValue},
		{"negative falls back", true, "-10", defaultValue},
		{"invalid falls back", true, "notanumber", defaultValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(maxErrorCommandDisplay.Name, tt.value)
			} else {
				_ = os.Unsetenv(maxErrorCommandDisplay.Name)
			}
			if got := GetMaxErrorCommandDisplay(); got != tt.want {
				t.Errorf("GetMaxErrorCommandDisplay() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetConcurrency(t *testing.T) {
	t.Setenv(concurrency.Name, os.Getenv(concurrency.Name))

	defaultValue, _ := strconv.Atoi(concurrency.DefaultValue)

	tests := []struct {
		name  string
		set   bool
		value string
		want  int
	}{
		{"default when unset", false, "", defaultValue},
		{"override", true, "8", 8},
		{"zero falls back", true, "0", defaultValue},
		{"negative falls back", true, "-2", defaultValue},
		{"invalid falls back", true, "xyz", defaultValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(concurrency.Name, tt.value)
			} else {
				_ = os.Unsetenv(concurrency.Name)
			}
			if got := GetConcurrency(); got != tt.want {
				t.Errorf("GetConcurrency() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsTimingsEnabled(t *testing.T) {
	t.Setenv(timings.Name, os.Getenv(timings.Name))

	tests := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"default when unset is false", false, "", false},
		{"one enables", true, "1", true},
		{"zero disables", true, "0", false},
		{"other number disables", true, "2", false},
		{"invalid disables", true, "yes", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(timings.Name, tt.value)
			} else {
				_ = os.Unsetenv(timings.Name)
			}
			if got := IsTimingsEnabled(); got != tt.want {
				t.Errorf("IsTimingsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStartupTimingsEnabled(t *testing.T) {
	t.Setenv(startupTimings.Name, os.Getenv(startupTimings.Name))

	tests := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"default when unset is false", false, "", false},
		{"one enables", true, "1", true},
		{"zero disables", true, "0", false},
		{"other number disables", true, "2", false},
		{"invalid disables", true, "yes", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(startupTimings.Name, tt.value)
			} else {
				_ = os.Unsetenv(startupTimings.Name)
			}
			if got := IsStartupTimingsEnabled(); got != tt.want {
				t.Errorf("IsStartupTimingsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The startup instrumentation is only golden-safe because internal/clitest
// strips every DATAMITSU_-prefixed variable from the blackbox environment.
func TestStartupTimingsIsDatamitsuPrefixed(t *testing.T) {
	if !strings.HasPrefix(startupTimings.Name, "DATAMITSU_") {
		t.Errorf("startup timings env var = %q, want a DATAMITSU_ prefix", startupTimings.Name)
	}
	if startupTimings.Name == timings.Name {
		t.Errorf("startup timings must not reuse %q", timings.Name)
	}
}

func TestIsForceGitSubprocessEnabled(t *testing.T) {
	t.Setenv(forceGitSubprocess.Name, os.Getenv(forceGitSubprocess.Name))

	tests := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"default when unset is false", false, "", false},
		{"one enables", true, "1", true},
		{"zero disables", true, "0", false},
		{"other number disables", true, "2", false},
		{"invalid disables", true, "always", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(forceGitSubprocess.Name, tt.value)
			} else {
				_ = os.Unsetenv(forceGitSubprocess.Name)
			}
			if got := IsForceGitSubprocessEnabled(); got != tt.want {
				t.Errorf("IsForceGitSubprocessEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The escape hatch is only golden-safe because internal/clitest strips every
// DATAMITSU_-prefixed variable from the blackbox environment.
func TestForceGitSubprocessIsDatamitsuPrefixed(t *testing.T) {
	if !strings.HasPrefix(forceGitSubprocess.Name, "DATAMITSU_") {
		t.Errorf("force git subprocess env var = %q, want a DATAMITSU_ prefix", forceGitSubprocess.Name)
	}
}

func TestEnvVarString(t *testing.T) {
	if got := concurrency.String(); got != concurrency.Name {
		t.Errorf("envVar.String() = %q, want %q", got, concurrency.Name)
	}
	// String() makes envVar usable directly in formatted error messages.
	if got := offline.String(); got != offline.Name {
		t.Errorf("envVar.String() = %q, want %q", got, offline.Name)
	}
}

func TestLibcOverride(t *testing.T) {
	t.Setenv(libcOverride.Name, os.Getenv(libcOverride.Name))

	t.Run("empty when unset", func(t *testing.T) {
		_ = os.Unsetenv(libcOverride.Name)
		if got := LibcOverride(); got != "" {
			t.Errorf("LibcOverride() = %q, want empty", got)
		}
	})

	t.Run("returns raw value", func(t *testing.T) {
		t.Setenv(libcOverride.Name, "musl")
		if got := LibcOverride(); got != "musl" {
			t.Errorf("LibcOverride() = %q, want %q", got, "musl")
		}
	})
}

func TestGetProjectBinPath(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/test-cache")

	t.Run("path shape and location", func(t *testing.T) {
		got, err := GetProjectBinPath("/home/user/myproject")
		if err != nil {
			t.Fatalf("GetProjectBinPath() error = %v", err)
		}
		want := filepath.Join(
			GetCachePath(),
			"projects",
			HashProjectPath("/home/user/myproject"),
			"bin",
		)
		if got != want {
			t.Errorf("GetProjectBinPath() = %q, want %q", got, want)
		}
	})

	t.Run("manifest is a sibling of the farm directory", func(t *testing.T) {
		binPath, err := GetProjectBinPath("/home/user/myproject")
		if err != nil {
			t.Fatalf("GetProjectBinPath() error = %v", err)
		}
		manifest, err := GetProjectManifestPath("/home/user/myproject")
		if err != nil {
			t.Fatalf("GetProjectManifestPath() error = %v", err)
		}
		if filepath.Dir(binPath) != filepath.Dir(manifest) {
			t.Errorf("manifest %q is not a sibling of bin dir %q", manifest, binPath)
		}
		if filepath.Base(manifest) != ProjectManifestFileName {
			t.Errorf("manifest base = %q, want %q", filepath.Base(manifest), ProjectManifestFileName)
		}
	})

	t.Run("distinct roots produce distinct paths, same root is stable", func(t *testing.T) {
		tests := []struct {
			name     string
			rootA    string
			rootB    string
			wantSame bool
		}{
			{name: "same root", rootA: "/home/user/a", rootB: "/home/user/a", wantSame: true},
			{name: "sibling roots", rootA: "/home/user/a", rootB: "/home/user/b", wantSame: false},
			{name: "nested root", rootA: "/home/user/a", rootB: "/home/user/a/sub", wantSame: false},
			{name: "case differs", rootA: "/home/user/a", rootB: "/home/user/A", wantSame: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				a, err := GetProjectBinPath(tt.rootA)
				if err != nil {
					t.Fatalf("GetProjectBinPath(%q) error = %v", tt.rootA, err)
				}
				b, err := GetProjectBinPath(tt.rootB)
				if err != nil {
					t.Fatalf("GetProjectBinPath(%q) error = %v", tt.rootB, err)
				}
				if (a == b) != tt.wantSame {
					t.Errorf("GetProjectBinPath(%q)=%q vs (%q)=%q: same=%v, want %v",
						tt.rootA, a, tt.rootB, b, a == b, tt.wantSame)
				}
			})
		}
	})

	t.Run("paths stay under the cache root and contain no dot segments", func(t *testing.T) {
		roots := []string{"/home/user/myproject", "/", "/a/b/../c"}
		for _, root := range roots {
			binPath, err := GetProjectBinPath(root)
			if err != nil {
				t.Fatalf("GetProjectBinPath(%q) error = %v", root, err)
			}
			manifest, err := GetProjectManifestPath(root)
			if err != nil {
				t.Fatalf("GetProjectManifestPath(%q) error = %v", root, err)
			}
			cacheRoot := GetCachePath() + string(filepath.Separator)
			for _, p := range []string{binPath, manifest} {
				if p != filepath.Clean(p) {
					t.Errorf("path %q is not clean", p)
				}
				if strings.Contains(p, ".."+string(filepath.Separator)) {
					t.Errorf("path %q contains a parent segment", p)
				}
				if !strings.HasPrefix(p, cacheRoot) {
					t.Errorf("path %q is not under cache root %q", p, cacheRoot)
				}
			}
		}
	})

	t.Run("empty and relative roots error", func(t *testing.T) {
		tests := []string{"", "relative/path", "./project", "..", "~/project"}
		for _, root := range tests {
			if got, err := GetProjectBinPath(root); err == nil {
				t.Errorf("GetProjectBinPath(%q) = %q, want error", root, got)
			}
			if got, err := GetProjectManifestPath(root); err == nil {
				t.Errorf("GetProjectManifestPath(%q) = %q, want error", root, got)
			}
		}
	})
}

func TestGetProjectBinPathHonorsCacheDirEnv(t *testing.T) {
	t.Setenv(cacheDir.Name, "/tmp/custom-cache")

	got, err := GetProjectBinPath("/home/user/myproject")
	if err != nil {
		t.Fatalf("GetProjectBinPath() error = %v", err)
	}
	if !strings.HasPrefix(got, "/tmp/custom-cache"+string(filepath.Separator)) {
		t.Errorf("GetProjectBinPath() = %q, want it under /tmp/custom-cache", got)
	}

	manifest, err := GetProjectManifestPath("/home/user/myproject")
	if err != nil {
		t.Fatalf("GetProjectManifestPath() error = %v", err)
	}
	if !strings.HasPrefix(manifest, "/tmp/custom-cache"+string(filepath.Separator)) {
		t.Errorf("GetProjectManifestPath() = %q, want it under /tmp/custom-cache", manifest)
	}
}
