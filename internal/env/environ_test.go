package env

import (
	"slices"
	"strings"
	"testing"
)

// TestEnviron_CollectsOnlyDatamitsuVars asserts the fingerprint sees datamitsu's
// own configuration and nothing else — an unrelated variable changing must not
// invalidate a farm.
func TestEnviron_CollectsOnlyDatamitsuVars(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	t.Setenv("DATAMITSU_NO_OCI", "1")
	t.Setenv("SOME_OTHER_TOOL_FLAG", "1")

	got := Environ()

	if !slices.Contains(got, "DATAMITSU_OFFLINE=1") {
		t.Errorf("Environ() = %v, want it to contain DATAMITSU_OFFLINE=1", got)
	}
	if !slices.Contains(got, "DATAMITSU_NO_OCI=1") {
		t.Errorf("Environ() = %v, want it to contain DATAMITSU_NO_OCI=1", got)
	}
	if slices.Contains(got, "SOME_OTHER_TOOL_FLAG=1") {
		t.Errorf("Environ() = %v, want unrelated variables excluded", got)
	}
}

// TestEnviron_IsSortedAndStable is what makes the value hashable: the process
// environment has no defined order, so an unsorted result would produce a
// different key on every call.
func TestEnviron_IsSortedAndStable(t *testing.T) {
	t.Setenv("DATAMITSU_OFFLINE", "1")
	t.Setenv("DATAMITSU_CONCURRENCY", "4")
	t.Setenv("DATAMITSU_MIN_RELEASE_AGE", "0")

	first := Environ()
	second := Environ()

	if !slices.IsSorted(first) {
		t.Errorf("Environ() = %v, want sorted", first)
	}
	if !slices.Equal(first, second) {
		t.Errorf("Environ() is not stable: %v vs %v", first, second)
	}
}

// TestEnviron_ValueChangeIsVisible asserts a changed value — not just a changed
// set of names — moves the result, since that is the transition the staleness
// key depends on.
func TestEnviron_ValueChangeIsVisible(t *testing.T) {
	t.Setenv("DATAMITSU_MIN_RELEASE_AGE", "10080")
	before := Environ()

	t.Setenv("DATAMITSU_MIN_RELEASE_AGE", "0")
	after := Environ()

	if slices.Equal(before, after) {
		t.Errorf("Environ() unchanged after a value change: %v", after)
	}
}

// TestEnviron_ExcludesActivationMarkers is the guard against a rebake loop: the
// variables `datamitsu source` exports into a shell describe which farm is
// active, so a farm baked before activation and a tool run after it must agree
// on the fingerprint.
func TestEnviron_ExcludesActivationMarkers(t *testing.T) {
	before := Environ()

	t.Setenv(SourceRootVarName(), "/repo")
	t.Setenv(SourceFarmVarName(), "/cache/projects/abc/bin")
	// The explicit-config farm's marker is on exactly the same footing: a
	// machine-level activation sets it in every shell, so a farm baked without it
	// would report itself stale from the moment it was activated.
	t.Setenv(SourceFarmConfigVarName(), "/home/u/.config/datamitsu/datamitsu.config.ts")

	after := Environ()

	if !slices.Equal(before, after) {
		t.Errorf("activation markers changed the fingerprint:\nbefore: %v\nafter:  %v", before, after)
	}
	for _, name := range []string{SourceRootVarName(), SourceFarmVarName(), SourceFarmConfigVarName()} {
		for _, kv := range after {
			if strings.HasPrefix(kv, name+"=") {
				t.Errorf("Environ() leaked an activation marker: %q", kv)
			}
		}
	}
}

// Whether a cache is consulted is not part of what datamitsu produces: folding
// it into the source-mode staleness key would rebake every farm on the machine
// the moment somebody toggled it.
func TestEnviron_ExcludesTheConfigCacheSwitch(t *testing.T) {
	t.Setenv(configCache.Name, "0")
	for _, kv := range Environ() {
		if strings.HasPrefix(kv, configCache.Name+"=") {
			t.Errorf("Environ() contains %q; the config-cache switch must not enter a fingerprint", kv)
		}
	}
}

// EnvironAll is the config-evaluation fingerprint: everything config JS can
// read through facts().env, minus the same observation-only variables.
func TestEnvironAll_CoversTheWholeEnvironmentButTheExclusions(t *testing.T) {
	t.Setenv("CI", "1")
	t.Setenv(configCache.Name, "0")
	t.Setenv(trace.Name, "1")

	all := EnvironAll()
	if !slices.Contains(all, "CI=1") {
		t.Error("EnvironAll() dropped CI, which the shared config branches on")
	}
	if !slices.IsSorted(all) {
		t.Error("EnvironAll() is not sorted; the value has to be hashable")
	}
	for _, kv := range all {
		if strings.HasPrefix(kv, configCache.Name+"=") || strings.HasPrefix(kv, trace.Name+"=") {
			t.Errorf("EnvironAll() contains %q; observation-only variables must not change a cache key", kv)
		}
	}
}
