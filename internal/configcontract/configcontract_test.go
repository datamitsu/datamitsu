package configcontract

import (
	"slices"
	"testing"
)

// TestCapabilitiesIsSorted pins the ordering contract: config JS and the CLI
// both read this list, and an unstable order would churn any output that prints
// it.
func TestCapabilitiesIsSorted(t *testing.T) {
	got := Capabilities()
	if !slices.IsSorted(got) {
		t.Errorf("Capabilities() = %v, want sorted", got)
	}
}

// TestCapabilitiesReturnsFreshSlice guards the goja boundary: the result is
// handed to a JS runtime, so a caller mutating it must not corrupt the package
// state every later call reads.
func TestCapabilitiesReturnsFreshSlice(t *testing.T) {
	first := Capabilities()
	if first == nil {
		t.Fatal("Capabilities() = nil; an empty set must still marshal as [] rather than null")
	}
	if len(first) == 0 {
		// Nothing to mutate in a build that publishes no capabilities. The
		// aliasing contract is still exercised end to end by
		// facts.TestCollectDoesNotAliasContractState, and becomes directly
		// observable here as soon as the first capability ships.
		return
	}

	first[0] = "mutated"
	if second := Capabilities(); slices.Contains(second, "mutated") {
		t.Errorf("Capabilities() aliases package state: second call = %v", second)
	}
}

// TestSupportsAgreesWithCapabilities keeps the Go-side question and the
// JS-side question answered from one list.
func TestSupportsAgreesWithCapabilities(t *testing.T) {
	published := Capabilities()

	for _, c := range []Capability{CapArity, CapGranularity} {
		want := slices.Contains(published, string(c))
		if got := Supports(c); got != want {
			t.Errorf("Supports(%q) = %v, but Capabilities() contains it = %v", c, got, want)
		}
	}
}

// TestUnimplementedCapabilitiesAreNotPublished is the release gate for this
// plan: a capability name may only appear once the behaviour it promises is
// complete, because configs in the wild branch on it and a premature name makes
// them take a branch this build cannot honour.
//
// Delete a tool from this list in the same change that implements it — R3 for
// "arity", R5 for "granularity". See
// docs/plans/2026-08-18-tool-invocation-granularity.md.
func TestUnimplementedCapabilitiesAreNotPublished(t *testing.T) {
	unimplemented := []Capability{CapGranularity}

	published := Capabilities()
	for _, c := range unimplemented {
		if slices.Contains(published, string(c)) {
			t.Errorf("capability %q is published but its behaviour is not implemented in this build; "+
				"publish it only in the release that implements it", c)
		}
	}
}

// TestArgPlaceholdersAreStable pins the vocabulary this build substitutes.
// Adding a name here without implementing its expansion turns a loud config
// error into a literal token silently reaching the tool.
func TestArgPlaceholdersAreStable(t *testing.T) {
	want := []string{"file", "files", "root", "cwd", "toolCache", "target"}
	if !slices.Equal(ArgPlaceholders, want) {
		t.Errorf("ArgPlaceholders = %v, want %v", ArgPlaceholders, want)
	}

	wantEnv := []string{"root", "cwd", "toolCache"}
	if !slices.Equal(EnvPlaceholders, wantEnv) {
		t.Errorf("EnvPlaceholders = %v, want %v", EnvPlaceholders, wantEnv)
	}
}
