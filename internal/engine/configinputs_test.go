package engine

import (
	"fmt"
	"testing"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// TestConfigInputsInjected verifies that datamitsuConfigInputs.minimumReleaseAgeMinutes
// matches the effective runtime config value. runtimeconfig.Init() is idempotent,
// so calling it here guarantees the engine reads the same cached Effective the
// test asserts against.
func TestConfigInputsInjected(t *testing.T) {
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init() error = %v", err)
	}
	eff, err := runtimeconfig.Get()
	if err != nil {
		t.Fatalf("runtimeconfig.Get() error = %v", err)
	}

	e, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`datamitsuConfigInputs.minimumReleaseAgeMinutes`)
	if err != nil {
		t.Fatalf("RunString(minimumReleaseAgeMinutes) error = %v", err)
	}
	if got := int(val.ToInteger()); got != eff.MinimumReleaseAgeMinutes {
		t.Errorf("minimumReleaseAgeMinutes = %d, want %d", got, eff.MinimumReleaseAgeMinutes)
	}
}

// TestConfigInputsIsPlainObject verifies datamitsuConfigInputs is a plain object
// (not a function, array, or null), so config JS can read its fields directly.
func TestConfigInputsIsPlainObject(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`
		const v = datamitsuConfigInputs;
		(typeof v === "object" && v !== null && !Array.isArray(v) && typeof v !== "function").toString();
	`)
	if err != nil {
		t.Fatalf("RunString(type check) error = %v", err)
	}
	if got := val.String(); got != "true" {
		t.Errorf("datamitsuConfigInputs plain-object check = %q, want %q", got, "true")
	}
}

// TestConfigInputsIsFrozen pins that JS cannot mutate the injected global. The
// frozen surface enforces the "config inputs are bounded and read-only"
// contract structurally.
func TestConfigInputsIsFrozen(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`Object.isFrozen(datamitsuConfigInputs)`)
	if err != nil {
		t.Fatalf("RunString(isFrozen) error = %v", err)
	}
	if !val.ToBoolean() {
		t.Fatal("datamitsuConfigInputs should be Object.isFrozen() === true")
	}

	// Mutation attempts must not change the object (strict mode throws, sloppy
	// mode silently fails — either way the value stays put).
	before, err := e.vm.RunString(`datamitsuConfigInputs.minimumReleaseAgeMinutes`)
	if err != nil {
		t.Fatalf("RunString(read before) error = %v", err)
	}
	_, err = e.vm.RunString(`
		try { datamitsuConfigInputs.minimumReleaseAgeMinutes = -1; } catch (_) {}
		try { datamitsuConfigInputs.malicious = "injected"; } catch (_) {}
		try { delete datamitsuConfigInputs.minimumReleaseAgeMinutes; } catch (_) {}
	`)
	if err != nil {
		t.Fatalf("RunString(mutation attempts) error = %v", err)
	}
	after, err := e.vm.RunString(`
		[datamitsuConfigInputs.minimumReleaseAgeMinutes, "malicious" in datamitsuConfigInputs].join("|")
	`)
	if err != nil {
		t.Fatalf("RunString(read after) error = %v", err)
	}
	want := fmt.Sprintf("%d|false", before.ToInteger())
	if got := after.String(); got != want {
		t.Errorf("post-mutation state = %q, want %q (freeze did not block writes)", got, want)
	}
}

// TestConfigInputsKeysAreInjectedInSortedOrder pins that the global enumerates
// its keys in deterministic (alphabetical) order. With one field today this is
// trivially satisfied, but the assertion guards the contract as the allowlist
// grows.
func TestConfigInputsKeysAreInjectedInSortedOrder(t *testing.T) {
	wantOrder := "minimumReleaseAgeMinutes"

	for i := range 5 {
		e, err := New("")
		if err != nil {
			t.Fatalf("iter %d: New() error = %v", i, err)
		}
		val, err := e.vm.RunString(`Object.keys(datamitsuConfigInputs).join(",")`)
		if err != nil {
			t.Fatalf("iter %d: RunString(keys) error = %v", i, err)
		}
		if got := val.String(); got != wantOrder {
			t.Errorf("iter %d: Object.keys order = %q, want %q", i, got, wantOrder)
		}
	}
}

// TestConfigInputsOnlyAllowlistedFieldsExposed pins that exactly
// minimumReleaseAgeMinutes is exposed and that the rest of the
// runtimeconfig.Effective snapshot (installTimeoutSeconds, logLevel, timings,
// concurrency, maxCmdLength, maxErrorCmdDisplay, maxParallelWorkers) is NOT
// leaked into the config JS input surface.
func TestConfigInputsOnlyAllowlistedFieldsExposed(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`"minimumReleaseAgeMinutes" in datamitsuConfigInputs`)
	if err != nil {
		t.Fatalf("RunString(allowlisted present) error = %v", err)
	}
	if !val.ToBoolean() {
		t.Error("minimumReleaseAgeMinutes should be present in datamitsuConfigInputs")
	}

	forbidden := []string{
		"installTimeoutSeconds",
		"logLevel",
		"timings",
		"concurrency",
		"maxCmdLength",
		"maxErrorCmdDisplay",
		"maxParallelWorkers",
	}
	for _, key := range forbidden {
		val, err := e.vm.RunString(fmt.Sprintf(`%q in datamitsuConfigInputs`, key))
		if err != nil {
			t.Fatalf("RunString(forbidden %q) error = %v", key, err)
		}
		if val.ToBoolean() {
			t.Errorf("forbidden key %q must NOT be exposed in datamitsuConfigInputs", key)
		}
	}

	// Pin the exact key set so any accidentally added field is caught.
	val, err = e.vm.RunString(`Object.keys(datamitsuConfigInputs).sort().join(",")`)
	if err != nil {
		t.Fatalf("RunString(key set) error = %v", err)
	}
	if got := val.String(); got != "minimumReleaseAgeMinutes" {
		t.Errorf("exposed key set = %q, want %q", got, "minimumReleaseAgeMinutes")
	}
}
