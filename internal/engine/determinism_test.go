package engine

import (
	"context"
	"testing"
	"time"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestObservedNonDeterminism(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantFlag   bool
		wantSource string
	}{
		{
			name:       "Date.now",
			script:     `var t = Date.now();`,
			wantFlag:   true,
			wantSource: sourceClock,
		},
		{
			name:       "Math.random",
			script:     `var r = Math.random();`,
			wantFlag:   true,
			wantSource: sourceRand,
		},
		{
			name:       "zero-arg new Date",
			script:     `var d = new Date();`,
			wantFlag:   true,
			wantSource: sourceClock,
		},
		{
			name:       "zero-arg Date call",
			script:     `var d = Date();`,
			wantFlag:   true,
			wantSource: sourceClock,
		},
		{
			name:     "deterministic config",
			script:   `var d = new Date(2020, 0, 1); var x = d.getFullYear() + 1;`,
			wantFlag: false,
		},
		{
			name:     "parsed date is deterministic",
			script:   `var d = new Date("2020-01-01T00:00:00Z"); var x = d.getTime();`,
			wantFlag: false,
		},
		{
			name:     "pure arithmetic",
			script:   `var x = [1, 2, 3].map(function (n) { return n * 2; });`,
			wantFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEngine(t)
			if _, err := e.RunWithTimeout(tt.script, 5*time.Second); err != nil {
				t.Fatalf("run script: %v", err)
			}
			if got := e.ObservedNonDeterminism(); got != tt.wantFlag {
				t.Errorf("ObservedNonDeterminism() = %v, want %v (source %q)",
					got, tt.wantFlag, e.NonDeterminismSource())
			}
			if got := e.NonDeterminismSource(); got != tt.wantSource {
				t.Errorf("NonDeterminismSource() = %q, want %q", got, tt.wantSource)
			}
		})
	}
}

// The recorders must be transparent: a config that computes with the clock or
// the random source still evaluates to usable values, it just does not get
// cached.
func TestNonDeterminismShimsAreTransparent(t *testing.T) {
	e := newTestEngine(t)

	script := `
		var now = Date.now();
		var d = new Date();
		var r = Math.random();
		var fixed = new Date(2020, 0, 1);
		var result = {
			nowIsNumber: typeof now === "number" && now > 1e12,
			dateIsDate: d instanceof Date,
			dateProto: Object.getPrototypeOf(d) === Date.prototype,
			dateAgrees: Math.abs(d.getTime() - now) < 5000,
			randomInRange: typeof r === "number" && r >= 0 && r < 1,
			stringCall: typeof Date() === "string",
			isoRoundTrip: new Date(d.toISOString()).getTime() === Math.floor(d.getTime()),
			fixedYear: fixed.getFullYear(),
			ctorName: Date.name,
			isFunction: typeof Date === "function",
			subclassWorks: (function () {
				class MyDate extends Date {}
				var m = new MyDate(2020, 0, 1);
				return m instanceof MyDate && m instanceof Date && m.getFullYear() === 2020;
			})(),
		};
		result;`

	val, err := e.RunWithTimeout(script, 5*time.Second)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}

	got, ok := val.Export().(map[string]any)
	if !ok {
		t.Fatalf("script result is %T, want map", val.Export())
	}

	for _, key := range []string{
		"nowIsNumber", "dateIsDate", "dateProto", "dateAgrees", "randomInRange",
		"stringCall", "isoRoundTrip", "isFunction", "subclassWorks",
	} {
		if got[key] != true {
			t.Errorf("%s = %v, want true", key, got[key])
		}
	}
	if year, _ := got["fixedYear"].(int64); year != 2020 {
		t.Errorf("fixedYear = %v, want 2020", got["fixedYear"])
	}
	if name, _ := got["ctorName"].(string); name != "Date" {
		t.Errorf("Date.name = %q, want %q", name, "Date")
	}
}

// Math.random must still be random: recording it may not collapse it to a
// constant, which would change what a config computes.
func TestNonDeterminismRandomStaysRandom(t *testing.T) {
	e := newTestEngine(t)

	val, err := e.RunWithTimeout(`
		var seen = {};
		for (var i = 0; i < 100; i++) { seen[Math.random()] = true; }
		Object.keys(seen).length;`, 5*time.Second)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if n := val.ToInteger(); n < 90 {
		t.Errorf("100 Math.random() calls produced %d distinct values, want ~100", n)
	}
}

// Two engines must not share the flag: a non-deterministic layer marks its own
// engine, and the chain-level decision is the OR over the chain (see cmd).
func TestNonDeterminismIsPerEngine(t *testing.T) {
	dirty := newTestEngine(t)
	clean := newTestEngine(t)

	if _, err := dirty.RunWithTimeout(`Math.random();`, 5*time.Second); err != nil {
		t.Fatalf("run script: %v", err)
	}
	if _, err := clean.RunWithTimeout(`1 + 1;`, 5*time.Second); err != nil {
		t.Fatalf("run script: %v", err)
	}

	if !dirty.ObservedNonDeterminism() {
		t.Error("dirty engine did not observe non-determinism")
	}
	if clean.ObservedNonDeterminism() {
		t.Errorf("clean engine observed %q", clean.NonDeterminismSource())
	}
}
