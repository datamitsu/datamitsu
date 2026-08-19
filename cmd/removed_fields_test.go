package cmd

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// evalConfig runs a config object literal and hands back the value the loader
// would have received from getConfig.
func evalConfig(t *testing.T, js string) (*goja.Runtime, goja.Value) {
	t.Helper()
	vm := goja.New()
	v, err := vm.RunString("(" + js + ")")
	if err != nil {
		t.Fatalf("eval config: %v", err)
	}
	return vm, v
}

// ExportTo drops keys the Go struct no longer has, so a config still setting
// batch used to load clean and run under different semantics with nothing said:
// batch: false on a per-project operation carrying {files} meant one process per
// file and now means one process taking the whole list. The warning is the only
// thing standing between that config and a silent change of behaviour.
func TestRemovedOperationFieldUses(t *testing.T) {
	tests := []struct {
		name  string
		js    string
		want  int
		match []string
	}{
		{
			name: "batch is reported per tool and operation",
			js: `{tools: {
				prettier: {operations: {
					fix:  {app: "prettier", args: ["--write", "{files}"], batch: false},
					lint: {app: "prettier", args: ["--check", "{files}"], batch: true},
				}},
			}}`,
			want:  2,
			match: []string{`tool "prettier" operation "fix"`, `tool "prettier" operation "lint"`},
		},
		{
			name: "the message says what to do instead",
			js:   `{tools: {eslint: {operations: {lint: {app: "eslint", batch: true}}}}}`,
			want: 1,
			match: []string{
				`"batch" was removed`,
				"dispatch now comes from arity",
				"{file}", "{files}",
			},
		},
		{
			name: "a clean config says nothing",
			js: `{tools: {
				prettier: {operations: {fix: {app: "prettier", args: ["--write", "{files}"]}}},
			}}`,
			want: 0,
		},
		// Every one of these reached the walk before the nil guards existed.
		{name: "no tools key", js: `{apps: {}}`, want: 0},
		{name: "null tools", js: `{tools: null}`, want: 0},
		{name: "tool without operations", js: `{tools: {solo: {}}}`, want: 0},
		{name: "null operations", js: `{tools: {solo: {operations: null}}}`, want: 0},
		{
			name: "batch outside an operation is not ours to judge",
			js:   `{tools: {solo: {batch: true, operations: {}}}}`,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm, val := evalConfig(t, tt.js)

			got := removedOperationFieldUses(vm, val)
			if len(got) != tt.want {
				t.Fatalf("reported %d use(s), want %d: %v", len(got), tt.want, got)
			}
			joined := strings.Join(got, "\n")
			for _, want := range tt.match {
				if !strings.Contains(joined, want) {
					t.Errorf("message set %v is missing %q", got, want)
				}
			}
		})
	}
}

// Reporting is not enough on its own: the config has to fail to load. This was
// a warning only while the published wrapper still shipped batch; that wrapper
// has been replaced, so a config still carrying the key is now rejected.
func TestParseConfigResultRejectsRemovedFields(t *testing.T) {
	t.Run("a config carrying batch does not load", func(t *testing.T) {
		vm, val := evalConfig(t, `{tools: {
			prettier: {operations: {fix: {app: "prettier", args: ["--write", "{files}"], batch: false}}},
		}}`)

		cfg, err := parseConfigResult(vm, val)
		if err == nil {
			t.Fatal("parseConfigResult accepted a config still setting batch")
		}
		if cfg != nil {
			t.Error("a rejected config was returned anyway")
		}
		for _, want := range []string{"removed fields", `tool "prettier"`, "arity"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %v does not mention %q", err, want)
			}
		}
	})

	t.Run("a clean config loads", func(t *testing.T) {
		vm, val := evalConfig(t, `{tools: {
			prettier: {operations: {fix: {app: "prettier", args: ["--write", "{files}"]}}},
		}}`)

		if _, err := parseConfigResult(vm, val); err != nil {
			t.Fatalf("parseConfigResult rejected a clean config: %v", err)
		}
	})
}

// Map iteration is random, so an unsorted report would reorder between runs and
// make the message unreadable in a diff or a CI log.
func TestRemovedOperationFieldUsesAreSorted(t *testing.T) {
	js := `{tools: {
		zebra:  {operations: {lint: {app: "z", batch: true}}},
		alpha:  {operations: {lint: {app: "a", batch: true}}},
		middle: {operations: {lint: {app: "m", batch: true}}},
	}}`

	for range 5 {
		vm, val := evalConfig(t, js)
		got := removedOperationFieldUses(vm, val)
		if len(got) != 3 {
			t.Fatalf("reported %d uses, want 3", len(got))
		}
		for i := 1; i < len(got); i++ {
			if got[i-1] > got[i] {
				t.Fatalf("report is unsorted at %d: %v", i, got)
			}
		}
	}
}
