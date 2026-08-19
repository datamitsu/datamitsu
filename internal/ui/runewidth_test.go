package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// go-runewidth v0.0.28 builds its lookup tables lazily instead of in init()
// (~33 ms saved at every process start). The tests below pin the column math
// that change could silently move: they assert through the decorators and the
// renderer the UI actually uses, so runewidth stays an indirect dependency and
// the assertions cover the real rendering path rather than the library API.

// cspell:ignore Dextra

// nameColumn renders the download bar's name decorator (Display.Download uses
// W: 24) for s. DextraSpace is used rather than DSyncSpaceR because the sync
// variants block on a width channel that only a running mpb container feeds;
// the padding arithmetic under test is identical.
func nameColumn(s string, w int) (string, int) {
	return decor.Name(s, decor.WC{W: w, C: decor.DextraSpace}).Decor(decor.Statistics{})
}

// TestNameColumnPadsByDisplayWidth asserts the name column is padded to a fixed
// number of terminal *columns*, not runes: a CJK name of 6 runes occupies 12
// columns and must receive 12 spaces of padding, not 18.
func TestNameColumnPadsByDisplayWidth(t *testing.T) {
	const column = 24

	tests := []struct {
		name    string
		in      string
		wantPad int // leading spaces, i.e. column - display width of in
	}{
		{"ascii", "actionlint", 14},
		{"cjk wide", "日本語ツール", 12},
		{"hangul wide", "한국어", 18},
		{"cjk mixed with ascii", "日本語 tool", 13},
		{"nfc latin", "café", 20},
		{"nfd latin combining mark", "café", 20},
		{"combining marks stacked", "à́̂", 23},
		{"emoji zwj sequence", "\U0001F469\u200d\U0001F4BB", 22},
		{"emoji plain", "\U0001F680", 22},
		{"ui symbols", "✓✗ℹ⬇⊘→", 18},
		{"empty", "", 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, view := nameColumn(tt.in, column)
			want := strings.Repeat(" ", tt.wantPad) + tt.in
			if got != want {
				t.Errorf("name column = %q, want %q", got, want)
			}
			if view != column {
				t.Errorf("view width = %d, want %d", view, column)
			}
		})
	}
}

// TestNameColumnKeepsSeparatorWhenOverflowing pins the DextraSpace contract
// documented in Display.Download: a name at or beyond the column width still
// gets exactly one separating space, so it never butts against the byte
// counters. Width is measured in columns, so a 13-rune CJK name overflows a
// 24-column field.
func TestNameColumnKeepsSeparatorWhenOverflowing(t *testing.T) {
	const column = 24

	tests := []struct {
		name     string
		in       string
		wantView int
	}{
		{"ascii exactly full", strings.Repeat("x", column), column + 1},
		{"ascii overflowing", "sops-v3.13.2.darwin.arm64", 26},
		{"cjk exactly full", strings.Repeat("あ", column/2), column + 1},
		{"cjk overflowing", strings.Repeat("あ", column/2+1), column + 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, view := nameColumn(tt.in, column)
			if got != " "+tt.in {
				t.Errorf("name column = %q, want %q", got, " "+tt.in)
			}
			if view != tt.wantView {
				t.Errorf("view width = %d, want %d", view, tt.wantView)
			}
		})
	}
}

// TestBarLineTruncatesWideRunesByDisplayWidth exercises the renderer's line
// truncation with a wide-rune name: the rendered line must be cut to the
// container width in columns and on a rune boundary, never mid-rune and never
// by rune count (which would leave a 2x-too-wide line wrapping the terminal).
func TestBarLineTruncatesWideRunesByDisplayWidth(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"cjk truncated", "日本語ツールの名前がとても長い", 20, "日本語ツールの名前…"},
		{"cjk truncated odd width", "日本語ツールの名前がとても長い", 21, "日本語ツールの名前が…"},
		{"ascii truncated", strings.Repeat("z", 40), 20, strings.Repeat("z", 19) + "…"},
		{"mixed truncated", "日本語-" + strings.Repeat("z", 30), 20, "日本語-" + strings.Repeat("z", 12) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			// WithAutoRefresh renders on demand (at Wait) rather than on a
			// 150 ms ticker, which keeps the assertion deterministic.
			p := mpb.New(mpb.WithWidth(tt.width), mpb.WithOutput(&buf), mpb.WithAutoRefresh())
			bar := p.AddBar(1, mpb.PrependDecorators(decor.Name(tt.in, decor.WC{W: 24, C: decor.DSyncSpaceR})))
			bar.IncrBy(1)
			p.Wait()

			got := strings.TrimRight(buf.String(), "\n")
			if got != tt.want {
				t.Errorf("rendered line = %q, want %q", got, tt.want)
			}
		})
	}
}
