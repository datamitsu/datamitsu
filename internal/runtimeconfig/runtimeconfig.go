// Package runtimeconfig is the single source of truth for datamitsu's effective
// runtime configuration: env-resolved values, execution limits, and runtime
// policy surface. It exposes a typed Effective struct (the public contract) for
// CLI introspection (datamitsu config runtime) and as the basis for the minimal
// allowlisted config-evaluation inputs injected into the JS VM.
//
// Dependency direction is one-way: runtimeconfig -> env. The env package uses
// literal fallback values and must NOT import runtimeconfig.
package runtimeconfig

import (
	"errors"
	"sync"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/target"
)

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
	Libc                     string `json:"libc"`
	LogFormat                string `json:"logFormat"`
	LogLevel                 string `json:"logLevel"`
	MaxCmdLength             int    `json:"maxCmdLength"`
	MaxErrorCmdDisplay       int    `json:"maxErrorCmdDisplay"`
	MaxParallelWorkers       int    `json:"maxParallelWorkers"`
	MinimumReleaseAgeMinutes int    `json:"minimumReleaseAgeMinutes"`
	NoOCI                    bool   `json:"noOci"`
	OCIRegistry              string `json:"ociRegistry"`
	Offline                  bool   `json:"offline"`
	Timings                  bool   `json:"timings"`
}

// Compute reads env getters and returns a fresh Effective. Pure function — no
// global state, no side effects. Tests use this directly. Libc is the one
// exception to "env getters only": it reports the effective host libc
// (DATAMITSU_LIBC override or detection) so seed-miss diagnostics can see the
// dimension that selects OCI bundle entries and store paths.
func Compute() Effective {
	return Effective{
		Concurrency:              env.GetConcurrency(),
		InstallTimeoutSeconds:    env.InstallTimeoutSeconds(),
		Libc:                     string(target.HostTarget().Libc),
		LogFormat:                env.GetLogFormat(),
		LogLevel:                 env.GetLogLevel().String(),
		MaxCmdLength:             env.GetMaxCommandLength(),
		MaxErrorCmdDisplay:       env.GetMaxErrorCommandDisplay(),
		MaxParallelWorkers:       env.GetMaxParallelWorkers(),
		MinimumReleaseAgeMinutes: env.MinimumReleaseAgeMinutes(),
		NoOCI:                    env.NoOCI(),
		OCIRegistry:              env.GetOCIRegistry(),
		Offline:                  env.Offline(),
		Timings:                  env.IsTimingsEnabled(),
	}
}

var (
	mu          sync.RWMutex
	effective   Effective
	initialized bool
)

// Init caches the Compute() result. It is idempotent — repeated calls are
// no-ops (not errors), which is safe for repeated Cobra command execution in
// tests and for embedded/daemon/watch workflows that may re-initialize.
func Init() error {
	mu.Lock()
	defer mu.Unlock()
	if initialized {
		return nil
	}
	effective = Compute()
	initialized = true
	return nil
}

// Get returns a copy of the cached Effective. It returns an error if Init() was
// not called. Caller mutation is safe — the returned struct is a copy and
// internal state is immutable after Init.
func Get() (Effective, error) {
	mu.RLock()
	defer mu.RUnlock()
	if !initialized {
		return Effective{}, errors.New("runtimeconfig: Get called before Init")
	}
	return effective, nil
}

// resetForTesting resets the cached state so tests can exercise the
// before-Init error path and re-run the Init lifecycle.
func resetForTesting() {
	mu.Lock()
	defer mu.Unlock()
	effective = Effective{}
	initialized = false
}
