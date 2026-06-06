package engine

import (
	"context"
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
)

// TestPNPMWorkspaceDefaultsInjected verifies that every key from
// pnpmdefaults.Defaults() is exposed as a JS global named
// pnpmWorkspaceDefaults with identical values. This is the contract config.js
// relies on to publish sharedStorage["pnpm-workspace-defaults"].
func TestPNPMWorkspaceDefaultsInjected(t *testing.T) {
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`pnpmWorkspaceDefaults`)
	if err != nil {
		t.Fatalf("RunString(pnpmWorkspaceDefaults) error = %v", err)
	}

	exported := val.Export()
	got, ok := exported.(map[string]any)
	if !ok {
		t.Fatalf("pnpmWorkspaceDefaults exported as %T, want map[string]any (value: %v)", exported, exported)
	}

	want := pnpmdefaults.Defaults()
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d (got: %v, want: %v)", len(got), len(want), got, want)
	}

	for key, wantVal := range want {
		gotVal, present := got[key]
		if !present {
			t.Errorf("key %q missing from injected global", key)
			continue
		}
		if !valuesEqual(gotVal, wantVal) {
			t.Errorf("key %q: got %v (%T), want %v (%T)", key, gotVal, gotVal, wantVal, wantVal)
		}
	}
}

// TestPNPMWorkspaceDefaultsIsPlainObject verifies that
// pnpmWorkspaceDefaults is a plain object (not a function or array or null),
// so JS code can read it directly and pass it through YAML.stringify without
// invoking it.
func TestPNPMWorkspaceDefaultsIsPlainObject(t *testing.T) {
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`
		const v = pnpmWorkspaceDefaults;
		(typeof v === "object" && v !== null && !Array.isArray(v) && typeof v !== "function").toString();
	`)
	if err != nil {
		t.Fatalf("RunString(type check) error = %v", err)
	}
	if got := val.String(); got != "true" {
		t.Errorf("pnpmWorkspaceDefaults plain-object check = %q, want %q", got, "true")
	}
}

// TestPNPMWorkspaceDefaultsKeysAreInjectedInSortedOrder pins that the JS
// global enumerates its keys in deterministic (alphabetical) order so
// YAML.stringify produces stable output across runs. Go map iteration is
// randomized, so this requires the injector to sort explicitly.
func TestPNPMWorkspaceDefaultsKeysAreInjectedInSortedOrder(t *testing.T) {
	wantOrder := "blockExoticSubdeps,dangerouslyAllowAllBuilds,enablePrePostScripts,lockfile,minimumReleaseAge,preferFrozenLockfile,strictDepBuilds,trustPolicy"

	// Run multiple times: a non-deterministic injector would produce
	// different orders across engines because each engine constructs its
	// own map and Go map iteration is randomized.
	for i := range 5 {
		e, err := New(context.Background(), "")
		if err != nil {
			t.Fatalf("iter %d: New() error = %v", i, err)
		}
		val, err := e.vm.RunString(`Object.keys(pnpmWorkspaceDefaults).join(",")`)
		if err != nil {
			t.Fatalf("iter %d: RunString(keys) error = %v", i, err)
		}
		if got := val.String(); got != wantOrder {
			t.Errorf("iter %d: Object.keys order = %q, want %q", i, got, wantOrder)
		}
	}
}

// TestPNPMWorkspaceDefaultsIsFrozen pins that JS cannot mutate the injected
// global. Without freezing, goja exposes the underlying Go map by reference
// and JS writes propagate back to Go, letting a malicious or buggy user
// config silently downgrade the published security defaults — exactly what
// this consolidation is meant to prevent.
func TestPNPMWorkspaceDefaultsIsFrozen(t *testing.T) {
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Object.isFrozen should report true.
	val, err := e.vm.RunString(`Object.isFrozen(pnpmWorkspaceDefaults)`)
	if err != nil {
		t.Fatalf("RunString(isFrozen) error = %v", err)
	}
	if !val.ToBoolean() {
		t.Fatal("pnpmWorkspaceDefaults should be Object.isFrozen() === true")
	}

	// Mutation attempts in strict mode must throw; in sloppy mode they
	// silently fail. Either way the value must not change.
	_, err = e.vm.RunString(`
		try { pnpmWorkspaceDefaults.strictDepBuilds = false; } catch (_) {}
		try { pnpmWorkspaceDefaults.malicious = "injected"; } catch (_) {}
		try { delete pnpmWorkspaceDefaults.minimumReleaseAge; } catch (_) {}
	`)
	if err != nil {
		t.Fatalf("RunString(mutation attempts) error = %v", err)
	}

	// Verify nothing changed.
	val, err = e.vm.RunString(`
		[
			pnpmWorkspaceDefaults.strictDepBuilds,
			pnpmWorkspaceDefaults.minimumReleaseAge,
			"malicious" in pnpmWorkspaceDefaults,
		].join("|");
	`)
	if err != nil {
		t.Fatalf("RunString(post-mutation read) error = %v", err)
	}
	want := "true|10080|false"
	if got := val.String(); got != want {
		t.Errorf("post-mutation state = %q, want %q (freeze did not block writes)", got, want)
	}
}

// TestPNPMWorkspaceDefaultsRoundTripsThroughYAMLStringify pins that the
// injected global survives YAML.stringify (the actual call config.js makes)
// and that the resulting YAML still parses back to the same key/value set.
func TestPNPMWorkspaceDefaultsRoundTripsThroughYAMLStringify(t *testing.T) {
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`YAML.stringify(pnpmWorkspaceDefaults)`)
	if err != nil {
		t.Fatalf("RunString(YAML.stringify) error = %v", err)
	}
	yamlOut := val.String()
	if yamlOut == "" {
		t.Fatal("YAML.stringify(pnpmWorkspaceDefaults) returned empty string")
	}

	// Round-trip through YAML.parse and compare key set.
	val, err = e.vm.RunString(`
		const out = YAML.stringify(pnpmWorkspaceDefaults);
		const back = YAML.parse(out);
		Object.keys(back).sort().join(",");
	`)
	if err != nil {
		t.Fatalf("RunString(round-trip) error = %v", err)
	}
	got := val.String()
	want := "blockExoticSubdeps,dangerouslyAllowAllBuilds,enablePrePostScripts,lockfile,minimumReleaseAge,preferFrozenLockfile,strictDepBuilds,trustPolicy"
	if got != want {
		t.Errorf("round-tripped keys = %q, want %q", got, want)
	}
}

// valuesEqual handles numeric type differences between Go literals (int) and
// the values goja exports back from JS (int64/float64) without losing
// precision for the integer-valued defaults we ship.
func valuesEqual(got, want any) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	if gi, ok := toInt64(got); ok {
		if wi, ok := toInt64(want); ok {
			return gi == wi
		}
	}
	return false
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		return int64(x), true
	case float64:
		if float64(int64(x)) == x {
			return int64(x), true
		}
	}
	return 0, false
}
