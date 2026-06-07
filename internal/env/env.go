// Package env centralizes typed access to datamitsu's environment variables and
// derived cache, store, and runtime paths.
package env

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/datamitsu/datamitsu/internal/ldflags"

	"go.uber.org/zap/zapcore"
)

func getBasePath() string {
	if dir := os.Getenv(cacheDir.Name); dir != "" {
		return dir
	}

	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, ldflags.PackageName)
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(homeDir, ".cache", ldflags.PackageName)
	}

	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, ldflags.PackageName)
	}

	return filepath.Join(os.TempDir(), ldflags.PackageName+"-cache")
}

// GetCachePath returns the cache directory ({base}/cache) for ephemeral data.
func GetCachePath() string {
	return filepath.Join(getBasePath(), "cache")
}

// GetStorePath returns the store directory ({base}/store) for downloaded artifacts.
func GetStorePath() string {
	return filepath.Join(getBasePath(), "store")
}

// GetBinPath returns the directory holding managed binary symlinks ({store}/.bin).
func GetBinPath() string {
	return filepath.Join(GetStorePath(), ".bin")
}

// GetLogLevel returns log level from environment variable
// Returns WarnLevel on parse error (matching the default)
func GetLogLevel() zapcore.Level {
	levelStr := logLevel.DefaultValue
	if envLevel := os.Getenv(logLevel.Name); envLevel != "" {
		levelStr = envLevel
	}

	var level zapcore.Level
	if err := level.UnmarshalText([]byte(levelStr)); err != nil {
		return zapcore.WarnLevel
	}
	return level
}

// GetMaxCommandLength returns maximum command line length for batching
// Returns default value on parse error
func GetMaxCommandLength() int {
	valueStr := maxCmdLength.DefaultValue
	if envValue := os.Getenv(maxCmdLength.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil || value <= 0 {
		defaultValue, _ := strconv.Atoi(maxCmdLength.DefaultValue)
		return defaultValue
	}
	return value
}

// GetMaxErrorCommandDisplay returns maximum command length for error display
// Returns default value on parse error
func GetMaxErrorCommandDisplay() int {
	valueStr := maxErrorCommandDisplay.DefaultValue
	if envValue := os.Getenv(maxErrorCommandDisplay.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil || value <= 0 {
		defaultValue, _ := strconv.Atoi(maxErrorCommandDisplay.DefaultValue)
		return defaultValue
	}
	return value
}

// GetMaxParallelWorkers returns maximum number of parallel workers
// Returns default value on parse error
func GetMaxParallelWorkers() int {
	valueStr := maxParallelWorkers.DefaultValue
	if envValue := os.Getenv(maxParallelWorkers.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil || value <= 0 {
		defaultValue, _ := strconv.Atoi(maxParallelWorkers.DefaultValue)
		return defaultValue
	}
	return value
}

// IsTimingsEnabled returns true if detailed timing display mode is enabled
func IsTimingsEnabled() bool {
	valueStr := timings.DefaultValue
	if envValue := os.Getenv(timings.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return false
	}
	return value == 1
}

// GetConcurrency returns number of concurrent binary downloads
// Returns default value on parse error
func GetConcurrency() int {
	valueStr := concurrency.DefaultValue
	if envValue := os.Getenv(concurrency.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil || value <= 0 {
		defaultValue, _ := strconv.Atoi(concurrency.DefaultValue)
		return defaultValue
	}
	return value
}

// InstallTimeoutSeconds returns the per-app install timeout in seconds.
// 0 is a valid value meaning "disabled" (no deadline).
// Negative or invalid values fall back to the default.
func InstallTimeoutSeconds() int {
	valueStr := installTimeout.DefaultValue
	if envValue := os.Getenv(installTimeout.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil || value < 0 {
		defaultValue, _ := strconv.Atoi(installTimeout.DefaultValue)
		return defaultValue
	}
	return value
}

// MinimumReleaseAgeMinutes returns the minimum release age in minutes.
// 0 is a valid value meaning "disabled" (no age filtering).
// Negative or invalid values fall back to the default.
func MinimumReleaseAgeMinutes() int {
	valueStr := minimumReleaseAge.DefaultValue
	if envValue := os.Getenv(minimumReleaseAge.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil || value < 0 {
		defaultValue, _ := strconv.Atoi(minimumReleaseAge.DefaultValue)
		return defaultValue
	}
	return value
}

// NoSponsor returns true if sponsor messages should be suppressed
func NoSponsor() bool {
	return os.Getenv(noSponsor.Name) != ""
}

// IsCI returns true if running in CI environment
// Checks for non-empty CI environment variable (standard across CI systems)
func IsCI() bool {
	return os.Getenv("CI") != ""
}

// GetBinaryCommandOverride returns custom binary command path override
// Returns empty string if not set
func GetBinaryCommandOverride() string {
	return os.Getenv(binaryCommandOverride.Name)
}
