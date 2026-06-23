package engine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestCallWithTimeout_Success(t *testing.T) {
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	v, err := e.vm.RunString(`(function(a, b) { return a + b; })`)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		t.Fatal("value is not callable")
	}

	res, err := e.CallWithTimeout(fn, time.Second, e.vm.ToValue(2), e.vm.ToValue(3))
	if err != nil {
		t.Fatalf("CallWithTimeout() error = %v", err)
	}
	if got := res.ToInteger(); got != 5 {
		t.Errorf("CallWithTimeout() = %d, want 5", got)
	}
}

func TestComputeRootPath(t *testing.T) {
	t.Run("git root provided", func(t *testing.T) {
		got, err := computeRootPath("/some/git/root")
		if err != nil {
			t.Fatalf("computeRootPath() error = %v", err)
		}
		if got != "/some/git/root" {
			t.Errorf("computeRootPath() = %q, want /some/git/root", got)
		}
	})

	t.Run("empty falls back to cwd", func(t *testing.T) {
		cwd, _ := os.Getwd()
		got, err := computeRootPath("")
		if err != nil {
			t.Fatalf("computeRootPath() error = %v", err)
		}
		if got != cwd {
			t.Errorf("computeRootPath() = %q, want cwd %q", got, cwd)
		}
	})
}

func TestYAMLRoundTrip_NestedArraysAndMaps(t *testing.T) {
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Nested arrays + nested objects exercise the []any and MapSlice recursion
	// branches of orderedAnyToGojaValue / convertGojaValueToOrderedStructure.
	script := `
		const parsed = YAML.parse("root:\n  list:\n    - a\n    - nested:\n        deep: 1\n  flag: true\n");
		YAML.stringify(parsed);
	`
	v, err := e.vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}
	out := v.String()
	// Assert keys AND values survive the round-trip, so a serializer that
	// dropped or mangled nested values would be caught.
	for _, want := range []string{"root", "list", "- a", "deep: 1", "flag: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("YAML round-trip output missing %q:\n%s", want, out)
		}
	}
}

func TestTOMLRoundTrip_NestedArraysAndMaps(t *testing.T) {
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Parse a TOML doc with a nested table and an array, then stringify it.
	// Exercises sortedMapToGojaValue (map + []any) and convertMapSliceToMap.
	script := `
		const parsed = TOML.parse("title = \"x\"\nitems = [1, 2, 3]\n\n[server]\nhost = \"localhost\"\nport = 8080\n");
		TOML.stringify(parsed);
	`
	v, err := e.vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString() error = %v", err)
	}
	out := v.String()
	// Assert keys AND values survive the round-trip, including the nested table
	// and the array, so a serializer that flattened nesting or dropped values
	// would be caught.
	for _, want := range []string{`title = 'x'`, "items = [1, 2, 3]", "[server]", `host = 'localhost'`, "port = 8080"} {
		if !strings.Contains(out, want) {
			t.Errorf("TOML round-trip output missing %q:\n%s", want, out)
		}
	}
}
