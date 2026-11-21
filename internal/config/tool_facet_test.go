package config

import (
	"sort"
	"strings"
	"testing"
)

func TestValidateToolFacets_EmptyAndNil(t *testing.T) {
	if err := ValidateToolFacets(nil); err != nil {
		t.Errorf("ValidateToolFacets(nil) unexpected error: %v", err)
	}
	if err := ValidateToolFacets(MapOfTools{}); err != nil {
		t.Errorf("ValidateToolFacets(empty) unexpected error: %v", err)
	}
}

func TestValidateToolFacets_WithFixOp(t *testing.T) {
	tools := MapOfTools{
		"gofmt": {
			ProjectTypes: nil,
			Skip:         false,
			SkipReason:   "",
			OutputParser: nil,
			Name:         "gofmt",
			Operations: map[OperationType]ToolOperation{
				OpFix: {Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "gofmt"},
			},
		},
	}
	if err := ValidateToolFacets(tools); err != nil {
		t.Errorf("ValidateToolFacets() unexpected error: %v", err)
	}
}

func TestValidateToolFacets_WithLintOp(t *testing.T) {
	tools := MapOfTools{
		"golangci-lint": {
			ProjectTypes: nil,
			Skip:         false,
			SkipReason:   "",
			OutputParser: nil,
			Name:         "golangci-lint",
			Operations: map[OperationType]ToolOperation{
				OpLint: {Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "golangci-lint"},
			},
		},
	}
	if err := ValidateToolFacets(tools); err != nil {
		t.Errorf("ValidateToolFacets() unexpected error: %v", err)
	}
}

func TestValidateToolFacets_NoOps(t *testing.T) {
	tools := MapOfTools{"empty": {ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil, Name: "empty"}}
	err := ValidateToolFacets(tools)
	if err == nil {
		t.Fatal("ValidateToolFacets() expected error for tool with no operations, got nil")
	}
	if !strings.Contains(err.Error(), `tool "empty": must declare at least one fix or lint operation`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateToolFacets_AggregatesAllErrors(t *testing.T) {
	tools := MapOfTools{
		"a": {ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil, Name: "a"},
		"b": {ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil, Name: "b"},
		"ok": {
			ProjectTypes: nil,
			Skip:         false,
			SkipReason:   "",
			OutputParser: nil,
			Name:         "ok",
			Operations:   map[OperationType]ToolOperation{OpFix: {Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil, App: "ok"}},
		},
	}
	err := ValidateToolFacets(tools)
	if err == nil {
		t.Fatal("ValidateToolFacets() expected aggregated errors, got nil")
	}
	for _, want := range []string{`tool "a"`, `tool "b"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %s; got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), `tool "ok"`) {
		t.Errorf("did not expect error for valid tool %q; got: %v", "ok", err)
	}
}

func TestLessLspByOrderThenName(t *testing.T) {
	tests := []struct {
		name string
		a, b LspSortable
		want bool
	}{
		{"lower order first", LspSortable{Name: "z", Order: 1}, LspSortable{Name: "a", Order: 2}, true},
		{"higher order not first", LspSortable{Name: "a", Order: 2}, LspSortable{Name: "z", Order: 1}, false},
		{"equal order alphabetical", LspSortable{Name: "a", Order: 5}, LspSortable{Name: "b", Order: 5}, true},
		{"equal order alphabetical reversed", LspSortable{Name: "b", Order: 5}, LspSortable{Name: "a", Order: 5}, false},
		{"identical not less", LspSortable{Name: "a", Order: 5}, LspSortable{Name: "a", Order: 5}, false},
		{"zero order ties alphabetical", LspSortable{Order: 0, Name: "alpha"}, LspSortable{Order: 0, Name: "beta"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lessLspByOrderThenName(tt.a, tt.b); got != tt.want {
				t.Errorf("lessLspByOrderThenName(%+v, %+v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestLessLspByOrderThenName_SortStability(t *testing.T) {
	entries := []LspSortable{
		{Name: "charlie", Order: 10},
		{Name: "alpha", Order: 10},
		{Name: "bravo", Order: 5},
		{Name: "delta", Order: 5},
	}
	sort.Slice(entries, func(i, j int) bool {
		return lessLspByOrderThenName(entries[i], entries[j])
	})
	want := []string{"bravo", "delta", "alpha", "charlie"}
	for i, w := range want {
		if entries[i].Name != w {
			t.Errorf("sorted[%d] = %q, want %q (full: %+v)", i, entries[i].Name, w, entries)
		}
	}
}
