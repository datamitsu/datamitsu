package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	clr "github.com/datamitsu/datamitsu/internal/color"
)

// PhaseRuleWidth is the fixed width of the ┏/┃/┗ bracket rules that frame an
// operation's output. Fixed (not terminal-derived) to keep output stable across
// pipes and terminals and to avoid an extra dependency.
const PhaseRuleWidth = 60

// RuleLine builds a bracket rule "<corner>━ <title> ━━━…" padded to
// PhaseRuleWidth. plainTitle drives the width (color codes are zero-width but
// would corrupt a rune count); coloredTitle is what gets rendered.
func RuleLine(corner, plainTitle, coloredTitle string) string {
	consumed := utf8.RuneCountInString(corner + "━ " + plainTitle + "  ")
	fill := max(PhaseRuleWidth-consumed, 3)
	return clr.Faint(corner+"━ ") + coloredTitle + clr.Faint(" "+strings.Repeat("━", fill))
}

// FormatDurationShort is a compact duration for result/footer lines (no ms tail).
func FormatDurationShort(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	seconds := float64(ms) / 1000.0
	if seconds < 60 {
		return fmt.Sprintf("%.2fs", seconds)
	}
	minutes := int(seconds) / 60
	return fmt.Sprintf("%dm%02ds", minutes, int(seconds)%60)
}

// PhaseOpen prints the opening bracket rule for an operation/phase, preceded by
// a blank line. The title is rendered bold.
func (d *Display) PhaseOpen(title string) {
	d.writeLine(d.out, "")
	d.writeLine(d.out, RuleLine("┏", title, clr.Bold(title)))
}

// PhaseBody prints one "┃ <s>" body line under an open phase. An empty s yields
// a bare "┃" spacer line.
func (d *Display) PhaseBody(s string) {
	if s == "" {
		d.writeLine(d.out, clr.Faint("┃"))
		return
	}
	d.writeLine(d.out, clr.Faint("┃ ")+s)
}

// PhaseClose prints the closing bracket rule that summarizes a phase. plain
// drives the rule width; colored is what gets rendered.
func (d *Display) PhaseClose(plain, colored string) {
	d.writeLine(d.out, RuleLine("┗", plain, colored))
}
