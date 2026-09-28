package textdiff

import (
	"fmt"
	"strconv"
	"strings"
)

// contextLines is how many unchanged lines surround a change in a hunk, as
// diff -u and git write it.
const contextLines = 3

// change is one run of lines before[a1:a2] replaced by after[b1:b2].
type change struct {
	a1, a2, b1, b2 int
}

// Unified returns the unified diff that turns before into after, as a patch
// of the file at path — "--- a/<path>", "+++ b/<path>", hunks with three lines
// of context — that `git apply` and `patch -p1` take; "" when the two are
// equal. It is computed while both texts exist: an edit keeps the new text
// only, so the patch cannot be rebuilt once the file was overwritten.
func Unified(path, before, after string) string {
	if before == after {
		return ""
	}
	a, b := splitLines(before), splitLines(after)
	changes := changesOf(a, b)
	if len(changes) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", path, path)
	for start := 0; start < len(changes); {
		end := start + 1
		for end < len(changes) && changes[end].a1-changes[end-1].a2 <= 2*contextLines {
			end++
		}
		writeHunk(&out, a, b, changes[start:end])
		start = end
	}
	return out.String()
}

// changesOf turns the edit script into runs of replaced lines: a deletion
// followed by an insertion at the same place is one run.
func changesOf(a, b []string) []change {
	var out []change
	for _, op := range operations(a, b) {
		switch op.Kind {
		case opDelete:
			out = append(out, change{a1: op.I1, a2: op.I2, b1: op.J1, b2: op.J1})
		case opInsert:
			if n := len(out); n > 0 && out[n-1].a2 == op.I1 && out[n-1].b2 == op.J1 {
				out[n-1].b2 = op.J1 + len(op.Content)
				continue
			}
			out = append(out, change{a1: op.I1, a2: op.I1, b1: op.J1, b2: op.J1 + len(op.Content)})
		case opEqual:
		}
	}
	return out
}

func writeHunk(out *strings.Builder, a, b []string, changes []change) {
	first, last := changes[0], changes[len(changes)-1]
	aStart := max(first.a1-contextLines, 0)
	aEnd := min(last.a2+contextLines, len(a))
	bStart := first.b1 - (first.a1 - aStart)
	bEnd := last.b2 + (aEnd - last.a2)
	fmt.Fprintf(out, "@@ -%s +%s @@\n", hunkRange(aStart, aEnd-aStart), hunkRange(bStart, bEnd-bStart))
	pos := aStart
	for _, c := range changes {
		for ; pos < c.a1; pos++ {
			writeLine(out, ' ', a[pos])
		}
		for i := c.a1; i < c.a2; i++ {
			writeLine(out, '-', a[i])
		}
		for j := c.b1; j < c.b2; j++ {
			writeLine(out, '+', b[j])
		}
		pos = c.a2
	}
	for ; pos < aEnd; pos++ {
		writeLine(out, ' ', a[pos])
	}
}

// hunkRange is "start,count" 1-based, "start" alone for one line, and the line
// before the hunk for an empty one.
func hunkRange(start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", start)
	case 1:
		return strconv.Itoa(start + 1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

func writeLine(out *strings.Builder, mark byte, line string) {
	out.WriteByte(mark)
	out.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		out.WriteString("\n\\ No newline at end of file\n")
	}
}
