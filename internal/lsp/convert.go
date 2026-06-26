package lsp

import (
	"fmt"

	"github.com/datamitsu/datamitsu/internal/textdiff"
)

// toTextEdits converts in-core line-based edits to LSP TextEdits. It returns a
// NON-NIL slice (possibly empty) so a no-op formatting response marshals as the
// JSON array [] rather than null, as textDocument/formatting requires.
//
// The conversion is a near-identity field copy because textdiff already emits
// zero-based line positions on line boundaries (Character==0). LSP Position
// characters are UTF-16 code units, which only matters for intra-line columns;
// at column 0 bytes, runes and UTF-16 units coincide, so no encoding math is
// performed. The guard fails loudly if a future textdiff change introduces
// non-zero columns, rather than silently emitting wrong UTF-16 offsets.
func toTextEdits(edits []textdiff.Edit) ([]TextEdit, error) {
	out := make([]TextEdit, 0, len(edits))
	for _, e := range edits {
		if e.Range.Start.Character != 0 || e.Range.End.Character != 0 {
			return nil, fmt.Errorf(
				"line-based edit carries a non-zero column (start.char=%d end.char=%d); LSP UTF-16 conversion is not implemented",
				e.Range.Start.Character, e.Range.End.Character)
		}
		out = append(out, TextEdit{
			Range: Range{
				Start: Position{Line: e.Range.Start.Line, Character: 0},
				End:   Position{Line: e.Range.End.Line, Character: 0},
			},
			NewText: e.NewText,
		})
	}
	return out, nil
}
