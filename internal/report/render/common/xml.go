package common

import (
	"strings"
	"unicode/utf8"
)

// The XML formats are written by hand rather than through encoding/xml, which
// escapes every line break in character data: a failure's list of findings
// would read as one line of character references.

// XMLText escapes s as XML character data: "&", "<" and ">" as entities, a
// carriage return as a reference so that a parser keeps it, and every
// character XML 1.0 cannot carry — most control characters, invalid UTF-8 —
// as U+FFFD.
func XMLText(s string) string {
	return escapeXML(s, false)
}

// XMLAttr escapes s as an XML attribute value in double quotes: XMLText's
// escapes, the quote, and tabs and line breaks as references, which attribute
// normalization would otherwise turn into spaces.
func XMLAttr(s string) string {
	return escapeXML(s, true)
}

func escapeXML(s string, attr bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"' && attr:
			b.WriteString("&quot;")
		case r == '\r':
			b.WriteString("&#xD;")
		case r == '\n' && attr:
			b.WriteString("&#xA;")
		case r == '\t' && attr:
			b.WriteString("&#x9;")
		case !xmlChar(r):
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// xmlChar reports a character XML 1.0 can carry.
func xmlChar(r rune) bool {
	return r == 0x9 || r == 0xA || r == 0xD ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}
