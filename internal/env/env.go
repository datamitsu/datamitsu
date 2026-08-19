// Package env centralizes typed access to datamitsu's environment variables and
// derived cache, store, and runtime paths.
package env

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// GetParsersPath returns the directory holding downloaded WASM parser modules
// ({store}/.parsers), or the DATAMITSU_PARSERS_DIR override when set.
func GetParsersPath() string {
	if dir := os.Getenv(parsersDir.Name); dir != "" {
		return dir
	}
	return filepath.Join(GetStorePath(), ".parsers")
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

// GetLogFormat returns the status output format ("console" or "jsonl").
// Unknown values fall back to the default ("console") rather than erroring, so a
// stray value never breaks output or the byte-stable CLI golden contract.
func GetLogFormat() string {
	value := logFormat.DefaultValue
	if envValue := os.Getenv(logFormat.Name); envValue != "" {
		value = strings.ToLower(strings.TrimSpace(envValue))
	}
	switch value {
	case "console", "jsonl":
		return value
	default:
		return logFormat.DefaultValue
	}
}

// GetLspFormatWidenTo returns the editor's format-on-save widening policy.
// Editors that send no initializationOptions — which is every shipped client
// today — fall back to this.
func GetLspFormatWidenTo() string {
	if v := os.Getenv(lspFormatWidenTo.Name); v != "" {
		return v
	}
	return lspFormatWidenTo.DefaultValue
}

// GetUnitCacheTTLMinutes returns how long a unit-level verdict stays trusted.
// Zero disables the verdict cache. Returns the default on parse error.
func GetUnitCacheTTLMinutes() int {
	valueStr := unitCacheTTLMinutes.DefaultValue
	if envValue := os.Getenv(unitCacheTTLMinutes.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil || value < 0 {
		defaultValue, _ := strconv.Atoi(unitCacheTTLMinutes.DefaultValue)
		return defaultValue
	}
	return value
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

// IsStartupTimingsEnabled returns true if per-phase startup instrumentation
// (config load, engine construction, type stripping, git-root lookup) should be
// recorded and reported. It is separate from IsTimingsEnabled, which covers the
// planner/runner stages: startup phases are measured before any command handler
// runs and are reported to stderr rather than the timing table.
func IsStartupTimingsEnabled() bool {
	valueStr := startupTimings.DefaultValue
	if envValue := os.Getenv(startupTimings.Name); envValue != "" {
		valueStr = envValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return false
	}
	return value == 1
}

// IsForceGitSubprocessEnabled returns true if git-root discovery must go
// through the git subprocess instead of the pure-Go filesystem walk. It is the
// documented escape hatch for a repository layout the walk refuses to answer
// for incorrectly rather than not at all — the walk already falls back on
// anything it does not recognise.
func IsForceGitSubprocessEnabled() bool {
	valueStr := forceGitSubprocess.DefaultValue
	if envValue := os.Getenv(forceGitSubprocess.Name); envValue != "" {
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

// GetOCIRegistry returns the OCI registry host used to resolve base image
// digests for generated Dockerfiles. Defaults to "ghcr.io" when unset.
func GetOCIRegistry() string {
	if value := os.Getenv(ociRegistry.Name); value != "" {
		return value
	}
	return ociRegistry.DefaultValue
}

// Offline returns true when all network access must be refused. Offline is
// orthogonal to OCI seeding: the store must be seeded separately while online.
func Offline() bool {
	return os.Getenv(offline.Name) != ""
}

// OfflineVarName returns the offline env var name for user-facing messages.
func OfflineVarName() string {
	return offline.Name
}

// NoOCI returns true if OCI bundle store seeding is disabled.
func NoOCI() bool {
	return os.Getenv(noOCI.Name) != ""
}

// NoParse returns true if output parsing is disabled (tools' raw output is shown
// instead of structured diagnostics) — the env twin of the --no-parse flag.
func NoParse() bool {
	return os.Getenv(noParse.Name) != ""
}

// LibcOverride returns the raw DATAMITSU_LIBC value ("" when unset). The
// target package validates it (glibc/musl) and applies it to host detection.
func LibcOverride() string {
	return os.Getenv(libcOverride.Name)
}

// SourceRootVarName returns the name of the variable an activated shell carries
// the source-mode git root in, so the shell renderer does not have to rebuild it
// from ldflags.PackageName.
func SourceRootVarName() string {
	return sourceRoot.Name
}

// SourceFarmVarName returns the name of the variable an activated shell carries
// the source-mode farm directory in.
func SourceFarmVarName() string {
	return sourceFarm.Name
}

// SourceRoot returns the git root of the farm activated in this shell, or "".
func SourceRoot() string {
	return os.Getenv(sourceRoot.Name)
}

// SourceFarm returns the farm directory activated in this shell, or "".
func SourceFarm() string {
	return os.Getenv(sourceFarm.Name)
}
