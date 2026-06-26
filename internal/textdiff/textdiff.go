// Package textdiff computes a minimal, line-based edit set between two strings
// using the Myers shortest-edit-sequence algorithm.
//
// It is the Go-core home for diffing: a WASM formatter emits the full new file
// text, and the core turns "old text vs new text" into the smallest set of edits
// that transforms one into the other. A minimal edit set (rather than a whole-file
// replacement) preserves untouched positions, which matters for undo history and
// for reusing these edits as LSP TextEdit ranges in a later phase.
//
// Edits are line-oriented: every Range starts and ends on a line boundary
// (Character == 0). Identical inputs return a nil slice (no edits). Because the
// diff works on whole lines, no per-column math is needed here; line contents are
// compared and copied as exact bytes, so multibyte lines round-trip unchanged.
// When character-level positions are introduced (Phase 3 LSP ranges), column math
// must use []rune so multibyte characters count as one column.
//
// The algorithm is ported from efm-langserver's diff.go, which in turn adapts
// golang.org/x/tools' Myers implementation.
package textdiff

import "strings"

// Position is a zero-based line/character location, matching the LSP convention
// so these edits can be reused as TextEdit ranges later.
type Position struct {
	Line      int
	Character int
}

// Range is a half-open span [Start, End) of text to replace.
type Range struct {
	Start Position
	End   Position
}

// Edit replaces the text covered by Range with NewText. For a line-based edit,
// Range.Start and Range.End sit on line boundaries (Character == 0): a deletion
// has an empty NewText and End.Line > Start.Line; an insertion is zero-width
// (Start == End) with NewText carrying the new line(s).
type Edit struct {
	Range   Range
	NewText string
}

// opKind denotes the type of operation a line range represents.
type opKind int

const (
	opDelete opKind = iota
	opInsert
	opEqual
)

// ComputeEdits returns the minimal set of line-based edits that transform before
// into after. Identical inputs return nil (no edits), matching the "no change →
// no edits" contract shared by the reference formatters.
func ComputeEdits(before, after string) []Edit {
	if before == after {
		return nil
	}
	ops := operations(splitLines(before), splitLines(after))
	edits := make([]Edit, 0, len(ops))
	for _, op := range ops {
		switch op.Kind {
		case opDelete:
			// before[I1:I2] is removed.
			edits = append(edits, Edit{Range: Range{
				Start: Position{Line: op.I1, Character: 0},
				End:   Position{Line: op.I2, Character: 0},
			}})
		case opInsert:
			// after[J1:J2] is inserted at before line I1.
			if content := strings.Join(op.Content, ""); content != "" {
				edits = append(edits, Edit{
					Range: Range{
						Start: Position{Line: op.I1, Character: 0},
						End:   Position{Line: op.I2, Character: 0},
					},
					NewText: content,
				})
			}
		case opEqual:
			// Unchanged lines are not emitted as edits.
		}
	}
	if len(edits) == 0 {
		return nil
	}
	return edits
}

// Apply applies a set of line-based edits (as produced by ComputeEdits) to before
// and returns the resulting text. Edits must be sorted by Range.Start.Line and
// non-overlapping, which is how ComputeEdits emits them. Line contents are copied
// byte-for-byte, so multibyte text is preserved exactly.
func Apply(before string, edits []Edit) string {
	if len(edits) == 0 {
		return before
	}
	lines := splitLines(before)
	var b strings.Builder
	prev := 0
	for _, e := range edits {
		for i := prev; i < e.Range.Start.Line && i < len(lines); i++ {
			b.WriteString(lines[i])
		}
		b.WriteString(e.NewText)
		prev = e.Range.End.Line
	}
	for i := prev; i < len(lines); i++ {
		b.WriteString(lines[i])
	}
	return b.String()
}

type operation struct {
	Kind    opKind
	Content []string // content from b
	I1, I2  int      // indices of the line in a
	J1      int      // index of the line in b; J2 implied by len(Content)
}

// operations returns the list of operations to convert a into b, consolidating
// runs of deletions/insertions and omitting equal lines.
func operations(a, b []string) []*operation {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}

	trace, offset := shortestEditSequence(a, b)
	snakes := backtrack(trace, len(a), len(b), offset)

	m, n := len(a), len(b)

	var i int
	solution := make([]*operation, len(a)+len(b))

	add := func(op *operation, i2, j2 int) {
		if op == nil {
			return
		}
		op.I2 = i2
		if op.Kind == opInsert {
			op.Content = b[op.J1:j2]
		}
		solution[i] = op
		i++
	}
	x, y := 0, 0
	for _, snake := range snakes {
		if len(snake) < 2 {
			continue
		}
		var op *operation
		// delete (horizontal)
		for snake[0]-snake[1] > x-y {
			if op == nil {
				op = &operation{
					Kind: opDelete,
					I1:   x,
					J1:   y,
				}
			}
			x++
			if x == m {
				break
			}
		}
		add(op, x, y)
		op = nil
		// insert (vertical)
		for snake[0]-snake[1] < x-y {
			if op == nil {
				op = &operation{
					Kind: opInsert,
					I1:   x,
					J1:   y,
				}
			}
			y++
		}
		add(op, x, y)
		op = nil
		// equal (diagonal)
		for x < snake[0] {
			x++
			y++
		}
		if x >= m && y >= n {
			break
		}
	}
	return solution[:i]
}

// backtrack walks the edit-sequence trace and returns the "snakes" that make up
// the solution. A snake is a single deletion or insertion followed by zero or
// more diagonals.
func backtrack(trace [][]int, x, y, offset int) [][]int {
	snakes := make([][]int, len(trace))
	d := len(trace) - 1
	for ; x > 0 && y > 0 && d > 0; d-- {
		v := trace[d]
		if len(v) == 0 {
			continue
		}
		snakes[d] = []int{x, y}

		k := x - y

		var kPrev int
		if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
			kPrev = k + 1
		} else {
			kPrev = k - 1
		}

		x = v[kPrev+offset]
		y = x - kPrev
	}
	if x < 0 || y < 0 {
		return snakes
	}
	snakes[d] = []int{x, y}
	return snakes
}

// shortestEditSequence returns the shortest edit sequence that converts a into b.
func shortestEditSequence(a, b []string) ([][]int, int) {
	m, n := len(a), len(b)
	v := make([]int, 2*(n+m)+1)
	offset := n + m
	trace := make([][]int, n+m+1)

	// Iterate through the maximum possible length of the SES (N+M).
	for d := 0; d <= n+m; d++ {
		copyV := make([]int, len(v))
		// k lines are represented by y = x - k; step by 2 because end points
		// for even d land on even k lines.
		for k := -d; k <= d; k += 2 {
			// At each point we go down (k == -d) or right (k == d), preferring
			// the larger x so deletions are favored over insertions.
			var x int
			if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
				x = v[k+1+offset] // down
			} else {
				x = v[k-1+offset] + 1 // right
			}

			y := x - k

			// Follow the diagonal while contents match.
			for x < m && y < n && a[x] == b[y] {
				x++
				y++
			}

			v[k+offset] = x

			if x == m && y == n {
				copy(copyV, v)
				trace[d] = copyV
				return trace, offset
			}
		}

		copy(copyV, v)
		trace[d] = copyV
	}
	return nil, 0
}

// splitLines splits text into lines, keeping the trailing newline on each line
// (so positions are preserved on apply) and dropping the empty element that
// SplitAfter produces after a trailing newline.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
