package engine

import (
	"context"
	"strings"
	"testing"
)

// runDistinct runs script n times and returns the set of distinct outputs.
func runDistinct(t *testing.T, e *Engine, script string, n int) map[string]struct{} {
	t.Helper()
	out := make(map[string]struct{})
	for range n {
		v, err := e.vm.RunString(script)
		if err != nil {
			t.Fatalf("RunString error = %v", err)
		}
		out[v.String()] = struct{}{}
	}
	return out
}

// assertOrder fails unless keys appear in got in the given order.
func assertOrder(t *testing.T, got string, keys ...string) {
	t.Helper()
	last := -1
	for _, k := range keys {
		idx := strings.Index(got, k)
		if idx < 0 {
			t.Fatalf("key %q missing from output:\n%s", k, got)
		}
		if idx <= last {
			t.Fatalf("key %q out of expected order:\n%s", k, got)
		}
		last = idx
	}
}

// TestYAMLSpreadMergeIsStable mirrors the real lefthook.yaml setup content
// function (datamitsu-config cmdSetup.ts): parse the existing file, spread its
// parsed commands into a new object, then re-stringify. This is the exact path
// that drifted the chain-hash — the spread of YAML.parse output exposed Go
// map-iteration order. With ordered parse it must be stable and source-ordered.
func TestYAMLSpreadMergeIsStable(t *testing.T) {
	engine, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const existing = `pre-commit:
  commands:
    docs-generate:
      priority: 3
      run: docs
      stage_fixed: true
    sync-datamitsu-version:
      priority: 2
      run: sync
      stage_fixed: true
    test:
      priority: 6
      run: pnpm test
    validate-blocklist:
      priority: 4
      run: validate
      stage_fixed: false
  parallel: false
`
	script := "(() => {\n" +
		"  const existing = YAML.parse(`" + existing + "`);\n" +
		"  return YAML.stringify({\n" +
		"    ...existing,\n" +
		"    'pre-commit': {\n" +
		"      commands: {\n" +
		"        ...(existing && existing['pre-commit'] && existing['pre-commit'].commands),\n" +
		"        'dm-check': { priority: 2, run: 'check', stage_fixed: true },\n" +
		"        'dm-init': { priority: 1, run: 'init' },\n" +
		"      },\n" +
		"      parallel: false,\n" +
		"    },\n" +
		"  });\n" +
		"})()"

	got := runDistinct(t, engine, script, 100)
	if len(got) != 1 {
		t.Fatalf("spread-merge unstable: %d distinct outputs", len(got))
	}
	for s := range got {
		// Existing commands keep file order, then the two appended ones.
		assertOrder(t, s, "docs-generate", "sync-datamitsu-version", "test",
			"validate-blocklist", "dm-check", "dm-init")
	}
}

// TestYAMLRoundTripPreservesOrder guards against the chain-hash drift bug:
// YAML.parse used to decode mappings into an unordered map[string]any, so a
// parse -> stringify round-trip emitted keys in random Go map-iteration order
// and produced a different string (and content hash) on every run. YAML must be
// both stable AND source-order preserving.
func TestYAMLRoundTripPreservesOrder(t *testing.T) {
	engine, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const input = `pre-commit:
  commands:
    datamitsu-check:
      priority: 2
      run: a
    datamitsu-init:
      priority: 1
      run: b
    docs-generate:
      priority: 3
      run: c
    sync-datamitsu-version:
      priority: 2
      run: d
    test:
      priority: 6
      run: e
    validate-blocklist:
      priority: 4
      run: f
`
	script := "YAML.stringify(YAML.parse(`" + input + "`))"
	got := runDistinct(t, engine, script, 100)
	if len(got) != 1 {
		t.Fatalf("YAML unstable: %d distinct outputs", len(got))
	}
	for s := range got {
		// Source order, not sorted, not randomized.
		assertOrder(t, s, "datamitsu-check", "datamitsu-init", "docs-generate",
			"sync-datamitsu-version", "test", "validate-blocklist")
		assertOrder(t, s, "priority: 2", "run: a")
	}
}

// TestJSONRoundTripPreservesOrder confirms native JSON is stable and keeps
// source order (no fix needed; here as a regression guard).
func TestJSONRoundTripPreservesOrder(t *testing.T) {
	engine, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const input = `{"zeta":1,"alpha":2,"mike":3,"bravo":4,"yankee":5,"delta":6}`
	script := "JSON.stringify(JSON.parse(`" + input + "`))"
	got := runDistinct(t, engine, script, 100)
	if len(got) != 1 {
		t.Fatalf("JSON unstable: %d distinct outputs", len(got))
	}
	for s := range got {
		assertOrder(t, s, "zeta", "alpha", "mike", "bravo", "yankee", "delta")
	}
}

// TestINIRoundTripPreservesOrder confirms INI is stable and keeps section/key
// order (go-ini iterates ordered slices; no fix needed).
func TestINIRoundTripPreservesOrder(t *testing.T) {
	engine, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const input = `[zeta]
b = 1
a = 2
[alpha]
y = 3
x = 4
[mike]
n = 5
`
	script := "INI.stringify(INI.parse(`" + input + "`))"
	got := runDistinct(t, engine, script, 100)
	if len(got) != 1 {
		t.Fatalf("INI unstable: %d distinct outputs", len(got))
	}
	for s := range got {
		assertOrder(t, s, "[zeta]", "[alpha]", "[mike]")
		assertOrder(t, s, "b = 1", "a = 2") // key order within a section preserved
	}
}

// TestTOMLRoundTripIsDeterministic guards the TOML fix: the decoder yields an
// unordered map, which used to randomize top-level key order across runs. TOML
// normalizes to sorted keys (not source order) but must be deterministic.
func TestTOMLRoundTripIsDeterministic(t *testing.T) {
	engine, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const input = `zeta = 1
alpha = 2
mike = 3
bravo = 4
yankee = 5
delta = 6

[ravens]
x = 1

[apples]
y = 2
`
	script := "TOML.stringify(TOML.parse(`" + input + "`))"
	got := runDistinct(t, engine, script, 100)
	if len(got) != 1 {
		t.Fatalf("TOML unstable: %d distinct outputs", len(got))
	}
	for s := range got {
		// Sorted order at the top level (scalars sort among themselves).
		assertOrder(t, s, "alpha", "bravo", "delta", "mike", "yankee", "zeta")
		// Tables sorted too.
		assertOrder(t, s, "[apples]", "[ravens]")
	}
}
