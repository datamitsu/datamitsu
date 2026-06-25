package textdiff

import (
	"fmt"
	"strings"
	"testing"
)

func TestComputeEdits(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		after   string
		wantNil bool
	}{
		{name: "identical empty", before: "", after: "", wantNil: true},
		{name: "identical single line", before: "a\n", after: "a\n", wantNil: true},
		{
			name:    "identical multi line",
			before:  "a\nb\nc\n",
			after:   "a\nb\nc\n",
			wantNil: true,
		},
		{name: "pure insert at end", before: "a\nb\n", after: "a\nb\nc\n"},
		{name: "pure insert in middle", before: "a\nc\n", after: "a\nb\nc\n"},
		{name: "pure insert at start", before: "b\nc\n", after: "a\nb\nc\n"},
		{name: "pure delete at end", before: "a\nb\nc\n", after: "a\nb\n"},
		{name: "pure delete in middle", before: "a\nb\nc\n", after: "a\nc\n"},
		{name: "pure delete at start", before: "a\nb\nc\n", after: "b\nc\n"},
		{name: "replace one line", before: "a\nb\nc\n", after: "a\nX\nc\n"},
		{name: "replace all", before: "a\nb\nc\n", after: "x\ny\nz\n"},
		{name: "empty to content", before: "", after: "a\nb\n"},
		{name: "content to empty", before: "a\nb\n", after: ""},
		{
			name:   "no trailing newline added",
			before: "a\nb",
			after:  "a\nb\n",
		},
		{
			name:   "trailing newline removed",
			before: "a\nb\n",
			after:  "a\nb",
		},
		{
			name:   "multibyte replace",
			before: "café\nмир\n",
			after:  "café\nмир!\n",
		},
		{
			name:   "multibyte insert",
			before: "α\nγ\n",
			after:  "α\nβ\nγ\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edits := ComputeEdits(tt.before, tt.after)
			if tt.wantNil {
				if edits != nil {
					t.Fatalf("expected nil edits, got %d: %+v", len(edits), edits)
				}
				return
			}
			if edits == nil {
				t.Fatalf("expected edits, got nil")
			}
			// Applying the edits to before must reproduce after exactly.
			if got := Apply(tt.before, edits); got != tt.after {
				t.Fatalf("Apply round-trip mismatch:\n before=%q\n after =%q\n got   =%q\n edits =%+v",
					tt.before, tt.after, got, edits)
			}
			// Edits must be sorted by start line and non-overlapping.
			prev := -1
			for _, e := range edits {
				if e.Range.Start.Line < prev {
					t.Fatalf("edits not sorted by start line: %+v", edits)
				}
				if e.Range.End.Line < e.Range.Start.Line {
					t.Fatalf("edit end before start: %+v", e)
				}
				if e.Range.Start.Character != 0 || e.Range.End.Character != 0 {
					t.Fatalf("line-based edit must have Character 0: %+v", e)
				}
				prev = e.Range.End.Line
			}
		})
	}
}

func TestComputeEdits_NoChangeNoEdits(t *testing.T) {
	if got := ComputeEdits("same\ntext\n", "same\ntext\n"); got != nil {
		t.Fatalf("unchanged input must yield nil, got %+v", got)
	}
}

func TestApply_NoEditsReturnsOriginal(t *testing.T) {
	const original = "a\nb\nc\n"
	if got := Apply(original, nil); got != original {
		t.Fatalf("Apply(nil) = %q, want %q", got, original)
	}
}

// TestComputeEdits_LargeFileMinimalEdit asserts the diff is minimal: changing a
// single line in a large file produces edits that touch only a tiny fraction of
// the file, not a whole-file replacement.
func TestComputeEdits_LargeFileMinimalEdit(t *testing.T) {
	const n = 2000
	var before strings.Builder
	for i := range n {
		fmt.Fprintf(&before, "line %d\n", i)
	}
	after := strings.Replace(before.String(), "line 1000\n", "line CHANGED\n", 1)

	edits := ComputeEdits(before.String(), after)
	if edits == nil {
		t.Fatal("expected edits for a changed line")
	}

	// Sum the lines covered by all edits; a minimal diff touches only a handful.
	touched := 0
	for _, e := range edits {
		touched += e.Range.End.Line - e.Range.Start.Line
		touched += strings.Count(e.NewText, "\n")
	}
	if touched > 10 {
		t.Fatalf("diff not minimal: touched %d lines for a 1-line change (edits=%d)", touched, len(edits))
	}

	if got := Apply(before.String(), edits); got != after {
		t.Fatal("Apply did not reproduce the changed large file")
	}
}
