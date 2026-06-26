package lsp

import (
	"encoding/json"
	"testing"

	"github.com/datamitsu/datamitsu/internal/textdiff"
)

func pos(line int) textdiff.Position { return textdiff.Position{Line: line, Character: 0} }

func TestToTextEdits_MapsLineEdits(t *testing.T) {
	edits := []textdiff.Edit{
		// delete before[1:3]
		{Range: textdiff.Range{Start: pos(1), End: pos(3)}},
		// insert "x\n" at line 5
		{Range: textdiff.Range{Start: pos(5), End: pos(5)}, NewText: "x\n"},
	}
	got, err := toTextEdits(edits)
	if err != nil {
		t.Fatalf("toTextEdits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d edits, want 2", len(got))
	}
	if got[0].Range.Start.Line != 1 || got[0].Range.End.Line != 3 || got[0].NewText != "" {
		t.Errorf("delete edit mapped wrong: %+v", got[0])
	}
	if got[1].Range.Start.Line != 5 || got[1].Range.End.Line != 5 || got[1].NewText != "x\n" {
		t.Errorf("insert edit mapped wrong: %+v", got[1])
	}
	for _, e := range got {
		if e.Range.Start.Character != 0 || e.Range.End.Character != 0 {
			t.Errorf("expected character==0, got %+v", e.Range)
		}
	}
}

func TestToTextEdits_EmptyMarshalsAsArrayNotNull(t *testing.T) {
	got, err := toTextEdits(nil)
	if err != nil {
		t.Fatalf("toTextEdits(nil): %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("empty edits marshalled as %s, want []", b)
	}
}

func TestToTextEdits_RejectsNonZeroColumn(t *testing.T) {
	edits := []textdiff.Edit{
		{Range: textdiff.Range{
			Start: textdiff.Position{Line: 0, Character: 3},
			End:   textdiff.Position{Line: 0, Character: 5},
		}, NewText: "y"},
	}
	if _, err := toTextEdits(edits); err == nil {
		t.Error("expected error for non-zero column, got nil")
	}
}
