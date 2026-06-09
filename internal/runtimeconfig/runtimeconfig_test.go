package runtimeconfig

import (
	"encoding/json"
	"testing"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestConstants(t *testing.T) {
	if MinimumReleaseAgeMinutes != 10080 {
		t.Errorf("MinimumReleaseAgeMinutes = %d, want 10080", MinimumReleaseAgeMinutes)
	}
	if InstallTimeoutSeconds != 600 {
		t.Errorf("InstallTimeoutSeconds = %d, want 600", InstallTimeoutSeconds)
	}
}

func TestEffectiveJSONRoundTrip(t *testing.T) {
	in := Effective{
		Concurrency:              3,
		InstallTimeoutSeconds:    600,
		Libc:                     "glibc",
		LogLevel:                 "info",
		MaxCmdLength:             32000,
		MaxErrorCmdDisplay:       120,
		MaxParallelWorkers:       12,
		MinimumReleaseAgeMinutes: 10080,
		NoOCI:                    true,
		OCIRegistry:              "ghcr.io",
		Offline:                  true,
		Timings:                  false,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Required keys must be present in serialized output. No total-count guard:
	// adding a new default should not require updating this test.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	requiredKeys := []string{
		"concurrency",
		"installTimeoutSeconds",
		"libc",
		"logLevel",
		"maxCmdLength",
		"maxErrorCmdDisplay",
		"maxParallelWorkers",
		"minimumReleaseAgeMinutes",
		"noOci",
		"ociRegistry",
		"offline",
		"timings",
	}
	for _, k := range requiredKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("serialized output missing required key %q", k)
		}
	}

	var out Effective
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal to struct: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestComputeDefaults(t *testing.T) {
	eff := Compute()
	if eff.MinimumReleaseAgeMinutes != MinimumReleaseAgeMinutes {
		t.Errorf("MinimumReleaseAgeMinutes = %d, want %d", eff.MinimumReleaseAgeMinutes, MinimumReleaseAgeMinutes)
	}
	if eff.InstallTimeoutSeconds != InstallTimeoutSeconds {
		t.Errorf("InstallTimeoutSeconds = %d, want %d", eff.InstallTimeoutSeconds, InstallTimeoutSeconds)
	}
	if eff.OCIRegistry != "ghcr.io" {
		t.Errorf("OCIRegistry = %q, want \"ghcr.io\"", eff.OCIRegistry)
	}
}

func TestComputeEnvOverride(t *testing.T) {
	t.Setenv("DATAMITSU_MIN_RELEASE_AGE", "1440")
	t.Setenv("DATAMITSU_INSTALL_TIMEOUT", "1200")

	eff := Compute()
	if eff.MinimumReleaseAgeMinutes != 1440 {
		t.Errorf("MinimumReleaseAgeMinutes = %d, want 1440", eff.MinimumReleaseAgeMinutes)
	}
	if eff.InstallTimeoutSeconds != 1200 {
		t.Errorf("InstallTimeoutSeconds = %d, want 1200", eff.InstallTimeoutSeconds)
	}
}

func TestComputeOfflineAndNoOCIDefaults(t *testing.T) {
	eff := Compute()
	if eff.Offline {
		t.Error("Offline = true, want false by default")
	}
	if eff.NoOCI {
		t.Error("NoOCI = true, want false by default")
	}
}

func TestComputeOfflineAndNoOCIOverride(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	t.Setenv("DATAMITSU_NO_OCI", "1")

	eff := Compute()
	if !eff.Offline {
		t.Error("Offline = false, want true with DATAMITSU_OFFLINE set")
	}
	if !eff.NoOCI {
		t.Error("NoOCI = false, want true with DATAMITSU_NO_OCI set")
	}
}

func TestComputeLibcPresent(t *testing.T) {
	switch Compute().Libc {
	case "glibc", "musl", "unknown":
	default:
		t.Errorf("Libc = %q, want glibc, musl, or unknown", Compute().Libc)
	}
}

func TestGetBeforeInit(t *testing.T) {
	resetForTesting()
	if _, err := Get(); err == nil {
		t.Error("Get() before Init() should return an error")
	}
}

func TestInitIdempotent(t *testing.T) {
	resetForTesting()
	if err := Init(); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := Init(); err != nil {
		t.Errorf("second Init should be no-op, got error: %v", err)
	}
	if _, err := Get(); err != nil {
		t.Errorf("Get() after Init: %v", err)
	}
}

func TestInstallTimeoutMatchesConstant(t *testing.T) {
	if env.InstallTimeoutSeconds() != InstallTimeoutSeconds {
		t.Errorf("env.InstallTimeoutSeconds() = %d, want %d", env.InstallTimeoutSeconds(), InstallTimeoutSeconds)
	}
}
