package engine

import (
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/facts"

	"github.com/dop251/goja"
)

type jsvNested struct {
	Files map[string]string `json:"files"`
}

type jsvSample struct {
	Entries  map[string]jsvNested `json:"entries"`
	Names    []string             `json:"names"`
	Ptr      *jsvNested           `json:"ptr"`
	Plain    string               `json:"plain"`
	Hidden   string               `json:"-"`
	Untagged string
}

func newConfigVM() *goja.Runtime {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	return vm
}

func evalString(t *testing.T, vm *goja.Runtime, expr string) string {
	t.Helper()
	v, err := vm.RunString(expr)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return v.String()
}

// The whole point: enumeration order must be the same on every conversion, and
// it must be sorted rather than whatever Go's map iteration produced first.
func TestDeterministicValueSortsMapKeysStably(t *testing.T) {
	sample := &jsvSample{Entries: map[string]jsvNested{}}
	for _, name := range []string{"zeta", "alpha", "mu", "beta", "kappa", "omega", "delta", "iota"} {
		sample.Entries[name] = jsvNested{Files: map[string]string{"b": "2", "a": "1", "c": "3"}}
	}

	const want = "alpha,beta,delta,iota,kappa,mu,omega,zeta"
	for range 50 {
		vm := newConfigVM()
		_ = vm.Set("x", DeterministicValue(vm, sample))
		if got := evalString(t, vm, "Object.keys(x.entries).join(',')"); got != want {
			t.Fatalf("Object.keys(x.entries) = %q, want %q", got, want)
		}
		if got := evalString(t, vm, "Object.keys({...x.entries.alpha.files}).join(',')"); got != "a,b,c" {
			t.Fatalf("nested map keys = %q, want a,b,c", got)
		}
	}
}

// goja's own conversion is the baseline this must not drift from: the same
// fields, the same JSON, the same nil shapes — only the order is pinned.
func TestDeterministicValueMatchesGojaShapes(t *testing.T) {
	cases := map[string]*jsvSample{
		"zero": {},
		"populated": {
			Entries:  map[string]jsvNested{"a": {Files: map[string]string{"f": "1"}}},
			Names:    []string{"n1", "n2"},
			Ptr:      &jsvNested{},
			Plain:    "p",
			Hidden:   "h",
			Untagged: "u",
		},
	}

	for name, sample := range cases {
		t.Run(name, func(t *testing.T) {
			baseline := newConfigVM()
			_ = baseline.Set("x", sample)

			pinned := newConfigVM()
			_ = pinned.Set("x", DeterministicValue(pinned, sample))

			for _, expr := range []string{
				"JSON.stringify(x)",
				"Object.keys(x).join(',')",
				"typeof x.entries",
				"typeof x.names",
				"String(x.ptr)",
				"typeof x.hidden",
				"typeof x.untagged",
			} {
				if got, want := evalString(t, pinned, expr), evalString(t, baseline, expr); got != want {
					t.Errorf("%s = %q, goja gives %q", expr, got, want)
				}
			}
		})
	}
}

// A subtree that cannot reach a map is handed to goja untouched, so it keeps
// everything a Go-backed object has — including its methods.
type jsvWithMethod struct {
	Name string `json:"name"`
}

func (j jsvWithMethod) Shout() string { return strings.ToUpper(j.Name) }

func TestDeterministicValueLeavesMapFreeValuesGoBacked(t *testing.T) {
	if typeContainsMap(reflect.TypeFor[jsvWithMethod]()) {
		t.Fatal("jsvWithMethod has no map; typeContainsMap must be false")
	}

	vm := newConfigVM()
	_ = vm.Set("x", DeterministicValue(vm, jsvWithMethod{Name: "hi"}))
	if got := evalString(t, vm, "x.shout()"); got != "HI" {
		t.Errorf("x.shout() = %q, want HI; a map-free value must stay Go-backed", got)
	}
}

func TestDeterministicValueNil(t *testing.T) {
	vm := newConfigVM()
	_ = vm.Set("x", DeterministicValue(vm, nil))
	if got := evalString(t, vm, "String(x)"); got != "null" {
		t.Errorf("String(x) = %q, want null", got)
	}
}

// facts().env is a Go map read by config JS; JSON.stringify of it must not
// change between two evaluations that share a cache key.
func TestFactsEnvEnumerationIsSorted(t *testing.T) {
	e := newTestEngine(t)
	e.facts = &facts.Facts{Env: map[string]string{"ZZZ": "1", "AAA": "2", "MMM": "3"}}

	const want = "AAA,MMM,ZZZ"
	for range 20 {
		v, err := e.vm.RunString("Object.keys(facts().env).join(',')")
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := v.String(); got != want {
			t.Fatalf("Object.keys(facts().env) = %q, want %q", got, want)
		}
	}
}
