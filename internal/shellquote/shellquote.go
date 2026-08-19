// Package shellquote renders Go strings as literals that a specific shell
// parses back to the exact original byte sequence.
//
// It exists because source mode emits shell code containing filesystem paths
// that datamitsu does not control: a directory may legitimately contain a
// space, a single quote, a `[`, or a byte sequence that is not valid UTF-8.
// Naive quoting turns such a path into arbitrary command execution at
// activation time.
//
// Every function is pure: no filesystem access, no writers, no dependency on
// internal/ui. Output is always single-line and ASCII-only, so the result is
// safe to embed in a line-oriented shell script regardless of the locale the
// shell runs under.
package shellquote

import (
	"fmt"
	"strings"
)

// hexDigits is the lowercase hex alphabet used for byte escapes.
const hexDigits = "0123456789abcdef"

// Bash renders s as a bash/zsh literal using ANSI-C quoting ($'…').
//
// ANSI-C quoting is used unconditionally rather than only when needed: it is
// the one form in which every byte has a representation, so there is a single
// escaping rule to reason about instead of a "does this need quoting?"
// predicate that is the usual source of injection bugs. zsh understands the
// same syntax; bash 3.2 (the version macOS ships) does too.
//
// The result never contains a raw newline and never contains a byte outside
// printable ASCII.
//
// Bash panics if s contains a NUL byte. NUL is deliberately not silently
// dropped or encoded: a shell word cannot carry NUL — execve's argument
// vector is NUL-terminated — so any encoding would produce a literal that
// parses to something other than s. A NUL in a path or an app name is a bug
// upstream of this package, and truncating it silently is how a quoted path
// becomes a different path.
func Bash(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		panic("shellquote: cannot represent a NUL byte in a bash literal")
	}
	if s == "" {
		return "''"
	}

	var b strings.Builder
	b.Grow(len(s) + 4)
	b.WriteString("$'")
	for i := range len(s) {
		c := s[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\'':
			b.WriteString(`\'`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case isPrintableASCII(c):
			b.WriteByte(c)
		default:
			// bash consumes at most two hex digits after \x, so a following
			// literal hex character cannot extend the escape.
			writeHexEscape(&b, `\x`, c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// Fish renders s as a fish literal.
//
// fish has no ANSI-C quoting: inside single quotes only \' and \\ are
// recognized, so a control byte cannot be written there at all. The result is
// therefore a juxtaposition of single-quoted runs and unquoted \Xnn byte
// escapes — fish concatenates adjacent tokens with no separator, so
// 'a'\X0a'b' is one word. \X (as opposed to \x) sets a raw byte value, which
// is what makes non-UTF-8 input representable.
//
// The result never contains a raw newline and never contains a byte outside
// printable ASCII.
//
// Fish panics on a NUL byte, for the reason given on [Bash].
func Fish(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		panic("shellquote: cannot represent a NUL byte in a fish literal")
	}
	if s == "" {
		return "''"
	}

	var b strings.Builder
	b.Grow(len(s) + 4)
	inQuotes := false
	for i := range len(s) {
		c := s[i]
		if isPrintableASCII(c) {
			if !inQuotes {
				b.WriteByte('\'')
				inQuotes = true
			}
			if c == '\'' || c == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
			continue
		}
		if inQuotes {
			b.WriteByte('\'')
			inQuotes = false
		}
		writeHexEscape(&b, `\X`, c)
	}
	if inQuotes {
		b.WriteByte('\'')
	}
	return b.String()
}

// FishPathList renders dirs as a fish list literal — a space-separated
// sequence of quoted words, suitable for `set -gx PATH …`.
//
// fish's PATH is a list variable, not a colon-joined string, so joining with
// os.PathListSeparator would produce one element containing colons rather than
// several elements.
func FishPathList(dirs []string) string {
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = Fish(d)
	}
	return strings.Join(quoted, " ")
}

// isPrintableASCII reports whether c is a byte that every shell passes through
// a quoted word unchanged (modulo the quote and backslash characters, which
// callers escape themselves).
func isPrintableASCII(c byte) bool {
	return c >= 0x20 && c < 0x7f
}

// writeHexEscape appends prefix followed by c as exactly two lowercase hex
// digits. The fixed width matters: a variable-width escape could absorb a
// following hex character.
func writeHexEscape(b *strings.Builder, prefix string, c byte) {
	b.WriteString(prefix)
	b.WriteByte(hexDigits[c>>4])
	b.WriteByte(hexDigits[c&0x0f])
}

// String renders s for the named shell. Supported names are "bash", "zsh" and
// "fish"; anything else is an error naming the supported set.
func String(shell, s string) (string, error) {
	switch shell {
	case "bash", "zsh":
		return Bash(s), nil
	case "fish":
		return Fish(s), nil
	default:
		return "", fmt.Errorf("shellquote: unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
}
