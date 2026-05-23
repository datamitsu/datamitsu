package pnpmdefaults

import (
	"reflect"
	"sort"
	"testing"
)

// TestDefaultsExpectedKeyValues pins the 8 pnpm 11 workspace security
// defaults. Updating a value here is a deliberate policy change — the test
// failing means a key was renamed, removed, or had its value changed; the
// supply-chain-security guide must be updated alongside.
func TestDefaultsExpectedKeyValues(t *testing.T) {
	want := map[string]any{
		"strictDepBuilds":           true,
		"blockExoticSubdeps":        true,
		"enablePrePostScripts":      false,
		"dangerouslyAllowAllBuilds": false,
		"minimumReleaseAge":         10080,
		"trustPolicy":               "no-downgrade",
		"lockfile":                  true,
		"preferFrozenLockfile":      true,
	}
	got := Defaults()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Defaults() = %#v, want %#v", got, want)
	}
}

// TestDefaultsReturnsFreshCopy verifies the documented "new map per call"
// invariant. The contract is load-bearing: mergePNPMWorkspaceConfig mutates
// its returned map, and the engine injects it into a goja VM where a
// shared-reference regression would let one engine's mutations corrupt
// every subsequent caller's defaults.
func TestDefaultsReturnsFreshCopy(t *testing.T) {
	first := Defaults()
	first["strictDepBuilds"] = false
	first["injected"] = "evil"
	delete(first, "minimumReleaseAge")

	second := Defaults()
	if v, ok := second["strictDepBuilds"].(bool); !ok || !v {
		t.Errorf("second call strictDepBuilds = %v, want true (mutation leaked across calls)", second["strictDepBuilds"])
	}
	if _, present := second["injected"]; present {
		t.Error("second call contains 'injected' key (mutation leaked across calls)")
	}
	if _, present := second["minimumReleaseAge"]; !present {
		t.Error("second call missing minimumReleaseAge (deletion leaked across calls)")
	}
}

// TestDefaultsKeyCount guards against accidental key additions/removals.
// New keys are a policy decision and must be reflected here and in the
// supply-chain-security documentation.
func TestDefaultsKeyCount(t *testing.T) {
	got := Defaults()
	if len(got) != 8 {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Errorf("Defaults() returned %d keys, want 8 (keys: %v)", len(got), keys)
	}
}
