package engine

import (
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
)

// TestPNPMWorkspaceDefaultsInjected verifies that every key from
// pnpmdefaults.Defaults() is exposed as a JS global named
// pnpmWorkspaceDefaults with identical values. This is the contract config.js
// relies on to publish sharedStorage["pnpm-workspace-defaults"].
func TestPNPMWorkspaceDefaultsInjected(t *testing.T) {
	e, err := New("")
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
// pnpmWorkspaceDefaults is a plain object (not a function), so JS code can
// read it directly and pass it through YAML.stringify without invoking it.
func TestPNPMWorkspaceDefaultsIsPlainObject(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	val, err := e.vm.RunString(`typeof pnpmWorkspaceDefaults`)
	if err != nil {
		t.Fatalf("RunString(typeof) error = %v", err)
	}
	if got := val.String(); got != "object" {
		t.Errorf("typeof pnpmWorkspaceDefaults = %q, want %q", got, "object")
	}

	val, err = e.vm.RunString(`
		const keys = Object.keys(pnpmWorkspaceDefaults).sort();
		keys.join(",");
	`)
	if err != nil {
		t.Fatalf("RunString(keys) error = %v", err)
	}
	got := val.String()
	want := "blockExoticSubdeps,dangerouslyAllowAllBuilds,enablePrePostScripts,lockfile,minimumReleaseAge,preferFrozenLockfile,strictDepBuilds,trustPolicy"
	if got != want {
		t.Errorf("Object.keys(pnpmWorkspaceDefaults) sorted = %q, want %q", got, want)
	}
}

// TestPNPMWorkspaceDefaultsRoundTripsThroughYAMLStringify pins that the
// injected global survives YAML.stringify (the actual call config.js makes)
// and that the resulting YAML still parses back to the same key/value set.
func TestPNPMWorkspaceDefaultsRoundTripsThroughYAMLStringify(t *testing.T) {
	e, err := New("")
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
