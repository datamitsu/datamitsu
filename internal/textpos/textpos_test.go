package textpos

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSpans(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		col, endCol int
		unit        Unit
		chars       Span
		bytes       Span
		utf16       Span
		precision   Precision
	}{
		{
			name: "ascii", line: "var x = 1", col: 5, endCol: 6, unit: UTF16,
			chars: Span{5, 6}, bytes: Span{5, 6}, utf16: Span{5, 6}, precision: Exact,
		},
		// "é" is one code point, two UTF-8 bytes and one UTF-16 unit; "x"
		// follows it.
		{
			name: "é counted in bytes", line: "é x", col: 4, endCol: 5, unit: UTF8,
			chars: Span{3, 4}, bytes: Span{4, 5}, utf16: Span{3, 4}, precision: Exact,
		},
		{
			name: "é counted in code points", line: "é x", col: 3, endCol: 4, unit: UTF32,
			chars: Span{3, 4}, bytes: Span{4, 5}, utf16: Span{3, 4}, precision: Exact,
		},
		// U+1F600 is one code point, four UTF-8 bytes and two UTF-16 units.
		{
			name: "emoji counted in UTF-16", line: "😀x", col: 3, endCol: 4, unit: UTF16,
			chars: Span{2, 3}, bytes: Span{5, 6}, utf16: Span{3, 4}, precision: Exact,
		},
		{
			name: "emoji counted in code points", line: "😀x", col: 2, endCol: 3, unit: UTF32,
			chars: Span{2, 3}, bytes: Span{5, 6}, utf16: Span{3, 4}, precision: Exact,
		},
		{
			name: "a column inside a character is its start", line: "😀x", col: 2, endCol: 3, unit: UTF16,
			chars: Span{1, 2}, bytes: Span{1, 5}, utf16: Span{1, 3}, precision: Exact,
		},
		{
			name: "a tab is one unit", line: "\tx", col: 2, endCol: 3, unit: UTF8,
			chars: Span{2, 3}, bytes: Span{2, 3}, utf16: Span{2, 3}, precision: Exact,
		},
		{
			name: "a column past the end is clamped", line: "ab", col: 9, endCol: 12, unit: UTF8,
			chars: Span{3, 3}, bytes: Span{3, 3}, utf16: Span{3, 3}, precision: Exact,
		},
		{
			name: "column zero is the first", line: "ab", col: 0, endCol: 1, unit: UTF8,
			chars: Span{1, 1}, bytes: Span{1, 1}, utf16: Span{1, 1}, precision: Exact,
		},
		{
			name: "an unknown unit on an ASCII line", line: "var x", col: 5, endCol: 6,
			chars: Span{5, 6}, bytes: Span{5, 6}, utf16: Span{5, 6}, precision: ASCII,
		},
		{name: "an unknown unit on a line that is not ASCII", line: "é x", col: 3, endCol: 4, precision: Unknown},
		{
			name: "an invalid byte is one unit", line: string([]byte{0xff, 'x'}), col: 2, endCol: 3, unit: UTF16,
			chars: Span{2, 3}, bytes: Span{2, 3}, utf16: Span{2, 3}, precision: Exact,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chars, bytes, utf16, precision := Spans([]byte(tt.line), []byte(tt.line), tt.col, tt.endCol, tt.unit)
			if chars != tt.chars || bytes != tt.bytes || utf16 != tt.utf16 || precision != tt.precision {
				t.Errorf("Spans(%q, %d, %d, %q) = %v %v %v %s; want %v %v %v %s", tt.line, tt.col, tt.endCol, tt.unit,
					chars, bytes, utf16, precision, tt.chars, tt.bytes, tt.utf16, tt.precision)
			}
		})
	}
}

// The end column of a finding over several lines is counted on its last line.
func TestSpansAcrossLines(t *testing.T) {
	chars, bytes, utf16, precision := Spans([]byte("ab"), []byte("é😀z"), 2, 4, UTF32)
	if chars != (Span{2, 4}) || bytes != (Span{2, 8}) || utf16 != (Span{2, 5}) || precision != Exact {
		t.Errorf("Spans = %v %v %v %s", chars, bytes, utf16, precision)
	}
	if _, _, _, precision := Spans([]byte("ab"), []byte("é"), 1, 2, ""); precision != Unknown {
		t.Errorf("an unknown unit with a last line that is not ASCII = %s, want unknown", precision)
	}
}

func TestLines(t *testing.T) {
	tests := []struct {
		content string
		want    []string
	}{
		{"a\nb\n", []string{"a", "b"}},
		{"a\r\nb", []string{"a", "b"}},
		{"\xEF\xBB\xBFa\n\n", []string{"a", ""}},
		{"", nil},
		{"\n", []string{""}},
	}
	for _, tt := range tests {
		got := Lines([]byte(tt.content))
		var gotStrings []string
		for _, l := range got {
			gotStrings = append(gotStrings, string(l))
		}
		if !reflect.DeepEqual(gotStrings, tt.want) {
			t.Errorf("Lines(%q) = %q, want %q", tt.content, gotStrings, tt.want)
		}
	}
}

// A Reader loads a file once while it is unchanged and again once it changed.
func TestReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("\xEF\xBB\xBFone\r\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewReader()
	for row, want := range map[int]string{1: "one", 2: "two"} {
		got, err := r.LineAt(path, row)
		if err != nil || string(got) != want {
			t.Errorf("LineAt(%d) = %q, %v; want %q", row, got, err, want)
		}
	}
	if _, err := r.LineAt(path, 3); !errors.Is(err, ErrNoLine) {
		t.Errorf("LineAt(3) error = %v, want ErrNoLine", err)
	}

	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got, err := r.LineAt(path, 1); err != nil || string(got) != "changed" {
		t.Errorf("LineAt after a change = %q, %v; want the new line", got, err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LineAt(path, 1); err == nil {
		t.Error("LineAt of a deleted file returned a line")
	}
	if _, err := r.LineAt(t.TempDir(), 1); err == nil {
		t.Error("LineAt of a directory returned a line")
	}
}
