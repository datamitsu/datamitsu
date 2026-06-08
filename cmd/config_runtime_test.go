package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// TestConfigRuntimeCmd_ValidJSONWithRequiredKeys runs the command and asserts
// the output is valid JSON carrying the required keys. No total-count guard:
// adding a new runtime default must not break this test.
func TestConfigRuntimeCmd_ValidJSONWithRequiredKeys(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}

	buf := &bytes.Buffer{}
	configRuntimeCmd.SetOut(buf)
	t.Cleanup(func() { configRuntimeCmd.SetOut(nil) })

	if err := configRuntimeCmd.RunE(configRuntimeCmd, nil); err != nil {
		t.Fatalf("config runtime RunE: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	requiredKeys := []string{
		"concurrency",
		"installTimeoutSeconds",
		"logLevel",
		"maxCmdLength",
		"maxErrorCmdDisplay",
		"maxParallelWorkers",
		"minimumReleaseAgeMinutes",
		"ociRegistry",
		"timings",
	}
	for _, k := range requiredKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("runtime config JSON missing required key %q", k)
		}
	}
}

// TestConfigRuntimeCmd_EnvOverrideReflected verifies an env override flows into
// the serialized output. Init() caches once and is idempotent, so the
// env-sensitive assertion goes through Compute() (the documented approach for
// env-sensitive tests) feeding the same render path the command uses.
func TestConfigRuntimeCmd_EnvOverrideReflected(t *testing.T) {
	t.Setenv("DATAMITSU_INSTALL_TIMEOUT", "1200")
	t.Setenv("DATAMITSU_MIN_RELEASE_AGE", "1440")

	eff := runtimeconfig.Compute()
	out, err := runtimeConfigJSON(eff)
	if err != nil {
		t.Fatalf("runtimeConfigJSON: %v", err)
	}

	var got runtimeconfig.Effective
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}

	if got.InstallTimeoutSeconds != 1200 {
		t.Errorf("installTimeoutSeconds = %d, want 1200", got.InstallTimeoutSeconds)
	}
	if got.MinimumReleaseAgeMinutes != 1440 {
		t.Errorf("minimumReleaseAgeMinutes = %d, want 1440", got.MinimumReleaseAgeMinutes)
	}
}
