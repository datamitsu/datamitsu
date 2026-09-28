// Package textpos converts the columns a tool reports into every unit a
// consumer counts in — code points, UTF-8 bytes, UTF-16 code units — while the
// file is still on disk, so a report rendered later, elsewhere, or after the
// checkout changed never needs the source text again.
//
// Columns are 1-based and an end column is exclusive, as diagnostic.Diagnostic
// has them. A line is its bytes without the line terminator ("\n" or "\r\n"),
// and the first line without a UTF-8 byte-order mark: tools count from the
// first character after it.
package textpos

import (
	"unicode/utf8"
)

// Unit is what a column counts.
type Unit string

// Column units, as a parser module's descriptor names them. The empty unit is
// one nobody measured.
const (
	UTF8  Unit = "utf-8"
	UTF16 Unit = "utf-16"
	UTF32 Unit = "utf-32"
)

// Span is a start column on a finding's first line and an end column on its
// last line, 1-based, the end exclusive.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Precision says how far the converted columns can be trusted.
type Precision string

// Precisions of a conversion.
const (
	// Exact: the unit was known and the lines were read.
	Exact Precision = "exact"
	// ASCII: the unit was not known, but the lines are ASCII, where every unit
	// counts alike.
	ASCII Precision = "ascii"
	// Unknown: the unit was not known on a line that is not ASCII, or the
	// lines could not be read. There is nothing to convert; a consumer that
	// needs a column in another unit leaves it out.
	Unknown Precision = "unknown"
)

// Spans converts a finding's columns: col on start, its first line, and endCol
// on end, its last line (the same line for a one-line finding). A column past
// the end of its line is clamped to just after the last character, and one
// that falls inside a character to that character's start. The spans are
// zero when the precision is Unknown.
func Spans(start, end []byte, col, endCol int, unit Unit) (chars, bytes, utf16 Span, precision Precision) {
	switch unit {
	case UTF8, UTF16, UTF32:
		precision = Exact
	default:
		if !isASCII(start) || !isASCII(end) {
			return Span{}, Span{}, Span{}, Unknown
		}
		precision, unit = ASCII, UTF8
	}
	s := offset(start, col, unit)
	e := offset(end, endCol, unit)
	return Span{s.runes + 1, e.runes + 1}, Span{s.bytes + 1, e.bytes + 1}, Span{s.utf16 + 1, e.utf16 + 1}, precision
}

// position is a 0-based offset into a line in each unit.
type position struct {
	bytes, runes, utf16 int
}

// offset finds column col of line, counted in unit.
func offset(line []byte, col int, unit Unit) position {
	want := max(col-1, 0)
	var p position
	for p.bytes < len(line) {
		r, size := utf8.DecodeRune(line[p.bytes:])
		width := unitWidth(r, size, unit)
		if counted(p, unit)+width > want {
			// want is reached, or falls inside this character.
			return p
		}
		p.bytes += size
		p.runes++
		p.utf16 += utf16Width(r)
	}
	return p
}

func counted(p position, unit Unit) int {
	switch unit {
	case UTF8:
		return p.bytes
	case UTF16:
		return p.utf16
	case UTF32:
	}
	return p.runes
}

func unitWidth(r rune, size int, unit Unit) int {
	switch unit {
	case UTF8:
		return size
	case UTF16:
		return utf16Width(r)
	case UTF32:
	}
	return 1
}

// utf16Width is how many UTF-16 code units a rune takes; an invalid byte,
// which a tool reading the file as UTF-16 would see replaced, takes one.
func utf16Width(r rune) int {
	if r >= 0x10000 && r <= utf8.MaxRune {
		return 2
	}
	return 1
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
