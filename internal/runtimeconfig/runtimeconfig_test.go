package runtimeconfig

import (
	"encoding/json"
	"testing"
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
		LogLevel:                 "info",
		MaxCmdLength:             32000,
		MaxErrorCmdDisplay:       120,
		MaxParallelWorkers:       12,
		MinimumReleaseAgeMinutes: 10080,
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
		"logLevel",
		"maxCmdLength",
		"maxErrorCmdDisplay",
		"maxParallelWorkers",
		"minimumReleaseAgeMinutes",
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
