package tooling

import (
	"bytes"
	"testing"
)

func TestStripCSI(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "a.yaml:3:1: error", "a.yaml:3:1: error"},
		{"empty", "", ""},
		{"colour", "\x1b[31merror\x1b[0m: x", "error: x"},
		{"bold underline 256 colour", "\x1b[1;4m\x1b[38;5;208m[warn]\x1b[m", "[warn]"},
		{"cursor and erase", "\x1b[2K\x1b[1G[done]\x1b[?25h", "[done]"},
		{"intermediate byte", "a\x1b[1 qb", "ab"},
		{"cut off at the end", "ok\x1b[31", "ok"},
		{"malformed, the finding after it kept", "\x1b[31\na.yaml:3:1: [error] bad\n", "\na.yaml:3:1: [error] bad\n"},
		{"malformed by a byte out of range", "\x1b[1;\x80x", "\x80x"},
		{"bare introducer", "a\x1b[", "a"},
		{"lone escape kept", "a\x1bb", "a\x1bb"},
		{"escape at the end kept", "a\x1b", "a\x1b"},
		{"other escape kept", "\x1b]0;title\x07text", "\x1b]0;title\x07text"},
		{"multibyte text", "\x1b[33m⚠ — ok\x1b[0m", "⚠ — ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(StripCSI([]byte(tt.in))); got != tt.want {
				t.Errorf("StripCSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripCSILeavesItsInputAlone(t *testing.T) {
	in := []byte("\x1b[31merror\x1b[0m")
	raw := bytes.Clone(in)
	StripCSI(in)
	if !bytes.Equal(in, raw) {
		t.Errorf("StripCSI rewrote its input to %q", in)
	}
}
