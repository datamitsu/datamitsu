package tooling

import "bytes"

// stripCSI returns b without its ANSI control sequences (ESC [ parameters,
// intermediates, final byte). A sequence that never reaches a final byte loses
// what it had consumed and nothing more: the text after a malformed escape can
// be the finding a parser must see. b itself is never modified: callers keep
// the raw stream for the failure frame, and it can share memory with what they
// display.
func stripCSI(b []byte) []byte {
	i := bytes.IndexByte(b, 0x1b)
	if i < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for {
		out = append(out, b[:i]...)
		b = b[i:]
		if len(b) < 2 || b[1] != '[' {
			out = append(out, b[0])
			b = b[1:]
		} else {
			b = b[csiLen(b):]
		}
		i = bytes.IndexByte(b, 0x1b)
		if i < 0 {
			return append(out, b...)
		}
	}
}

// csiLen is the length of the sequence b starts with, ESC and '[' included.
// Without a final byte it ends before the first byte that cannot belong to it.
func csiLen(b []byte) int {
	n := 2
	for n < len(b) && b[n] >= 0x30 && b[n] <= 0x3f {
		n++
	}
	for n < len(b) && b[n] >= 0x20 && b[n] <= 0x2f {
		n++
	}
	if n < len(b) && b[n] >= 0x40 && b[n] <= 0x7e {
		return n + 1
	}
	return n
}
