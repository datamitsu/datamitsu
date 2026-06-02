// Package runtimeconfig is the single source of truth for datamitsu's effective
// runtime configuration: env-resolved values, execution limits, and runtime
// policy surface. It exposes a typed Effective struct (the public contract) for
// CLI introspection (datamitsu config runtime) and as the basis for the minimal
// allowlisted config-evaluation inputs injected into the JS VM.
//
// Dependency direction is one-way: runtimeconfig -> env. The env package uses
// literal fallback values and must NOT import runtimeconfig.
package runtimeconfig

// Compile-time defaults. These are the canonical default values; env getters
// fall back to them when the corresponding env var is unset or invalid.
const (
	// MinimumReleaseAgeMinutes is the default minimum age (in minutes) a release
	// must have before datamitsu will select it. 10080 minutes == 7 days.
	MinimumReleaseAgeMinutes = 10080

	// InstallTimeoutSeconds is the default per-app install timeout in seconds.
	InstallTimeoutSeconds = 600
)

// Effective is the full effective runtime configuration snapshot. It is the
// public API of this package and is serialized directly for CLI introspection.
// There is intentionally no ToMap() method — map conversion (for the JS VM) is
// internal to the engine layer via json.Marshal/json.Unmarshal.
type Effective struct {
	Concurrency              int    `json:"concurrency"`
	InstallTimeoutSeconds    int    `json:"installTimeoutSeconds"`
	LogLevel                 string `json:"logLevel"`
	MaxCmdLength             int    `json:"maxCmdLength"`
	MaxErrorCmdDisplay       int    `json:"maxErrorCmdDisplay"`
	MaxParallelWorkers       int    `json:"maxParallelWorkers"`
	MinimumReleaseAgeMinutes int    `json:"minimumReleaseAgeMinutes"`
	Timings                  bool   `json:"timings"`
}
