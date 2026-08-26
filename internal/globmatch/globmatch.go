// Package globmatch matches repository-relative paths against a list of glob
// patterns, preparing each pattern once so the per-path cost is as small as the
// pattern allows.
//
// It exists because datamitsu's two hottest loops are the same loop: the planner
// matches every tool's globs against every file in the repository, and project
// detection matches every project type's markers against the same list. Both are
// O(paths × patterns), so the constant factor of a single match is what decides
// whether planning a large monorepo takes 20 ms or 2 s.
//
// # What it does
//
// Every pattern is classified once, at construction:
//
//   - `**/*<literal>` — a suffix test. `**/` matches any run of leading
//     directories including none, `*` matches any run of non-separator
//     characters including none and including a leading dot, and the rest is
//     literal, so the pattern matches a path exactly when the path ends with
//     that literal.
//   - `**/<literal>` — a final-segment test: the path is that literal, or ends
//     with a separator followed by it.
//   - a pattern with no metacharacters at all — string equality.
//   - anything else — doublestar, unchanged, plus a cheap necessary-condition
//     pre-filter when every alternative in the pattern ends in a fixed
//     extension.
//
// The fast paths are equivalences, not heuristics, and
// TestFastPathsMatchDoublestar pins each of them against the real matcher over a
// corpus of awkward paths. A pattern whose shape is not recognised is never
// guessed at — it goes to doublestar.
package globmatch

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// metacharacters are the glob-significant bytes. A pattern fragment containing
// none of them is a literal, which is what every fast path depends on.
const metacharacters = `*?[]{}\`

// kind is how a prepared pattern is evaluated.
type kind uint8

const (
	kindDoublestar kind = iota // ask doublestar
	kindSuffix                 // `**/*<literal>` -> HasSuffix
	kindSegment                // `**/<literal>`  -> final path segment equals literal
	kindLiteral                // `<literal>`     -> whole path equals literal
)

// pattern is one prepared glob.
type pattern struct {
	kind kind
	raw  string
	// text is the literal the fast paths compare against: the suffix, the final
	// segment, or the whole path.
	text string
	// extensions is a necessary-condition pre-filter for kindDoublestar
	// patterns whose alternatives all end in a fixed extension; nil when none
	// can be proven.
	extensions []string
}

// Set is a prepared list of patterns. The zero Set matches nothing.
type Set struct {
	patterns []pattern
}

// New prepares a glob list for repeated matching.
func New(globs []string) Set {
	set := Set{patterns: make([]pattern, 0, len(globs))}
	for _, g := range globs {
		set.patterns = append(set.patterns, prepare(g))
	}
	return set
}

// Len returns how many patterns the set holds.
func (s Set) Len() int { return len(s.patterns) }

// Match reports whether rel matches any pattern in the set. rel must be a
// repository-relative, slash-separated path.
func (s Set) Match(rel string) bool {
	for i := range s.patterns {
		if s.patterns[i].match(rel) {
			return true
		}
	}
	return false
}

func prepare(glob string) pattern {
	switch {
	case strings.HasPrefix(glob, "**/*"):
		if tail := glob[len("**/*"):]; tail != "" && !strings.ContainsAny(tail, metacharacters) {
			return pattern{kind: kindSuffix, raw: glob, text: tail}
		}
	case strings.HasPrefix(glob, "**/"):
		// A final-segment literal. It must not contain a separator: `**/a/b`
		// anchors `a` to a directory boundary the segment test cannot express.
		if tail := glob[len("**/"):]; tail != "" &&
			!strings.ContainsAny(tail, metacharacters) && !strings.Contains(tail, "/") {
			return pattern{kind: kindSegment, raw: glob, text: tail}
		}
	case !strings.ContainsAny(glob, metacharacters):
		return pattern{kind: kindLiteral, raw: glob, text: glob}
	}
	return pattern{kind: kindDoublestar, raw: glob, extensions: Extensions(glob)}
}

func (p *pattern) match(rel string) bool {
	switch p.kind {
	case kindSuffix:
		return strings.HasSuffix(rel, p.text)
	case kindSegment:
		return rel == p.text || strings.HasSuffix(rel, "/"+p.text)
	case kindLiteral:
		return rel == p.text
	case kindDoublestar:
		// The extension list is a necessary condition only: a path ending in
		// none of them cannot match, but one that does still has to be matched.
		if p.extensions != nil && !hasAnySuffix(rel, p.extensions) {
			return false
		}
		matched, err := doublestar.Match(p.raw, rel)
		return err == nil && matched
	}
	// Unreachable: prepare assigns exactly one of the four kinds above. Spelling
	// every case out rather than using a default is what makes adding a fifth a
	// compile-time conversation instead of a silent fall-through to doublestar.
	return false
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

// Extensions returns the file extensions a single pattern can match, or nil when
// the pattern cannot be reduced to a fixed set. It understands the two shapes
// tool configs actually use: `**/*.go` and `**/*.{ts,tsx,js}`.
//
// The result is a *necessary* condition for a match, never a sufficient one, so
// callers may use it to reject but never to accept.
func Extensions(glob string) []string {
	// Only the last path segment can carry the extension.
	filename := glob
	if i := strings.LastIndexByte(glob, '/'); i >= 0 {
		filename = glob[i+1:]
	}

	// Must start with "*." to be an extension pattern.
	if len(filename) < 3 || filename[0] != '*' || filename[1] != '.' {
		return nil
	}
	rest := filename[2:]

	// Brace expansion: {ts,tsx,js}
	if len(rest) > 2 && rest[0] == '{' && rest[len(rest)-1] == '}' {
		inner := rest[1 : len(rest)-1]
		if inner == "" {
			return nil
		}
		var out []string
		for part := range strings.SplitSeq(inner, ",") {
			if part == "" || strings.ContainsAny(part, metacharacters) {
				return nil
			}
			out = append(out, "."+part)
		}
		return out
	}

	if strings.ContainsAny(rest, metacharacters) {
		return nil
	}
	return []string{"." + rest}
}

// ExtensionsAll returns the union of Extensions over every pattern, or nil if
// any pattern cannot be reduced. Callers use it to decide whether two glob lists
// can be proven disjoint.
func ExtensionsAll(globs []string) map[string]bool {
	out := make(map[string]bool)
	for _, g := range globs {
		exts := Extensions(g)
		if exts == nil {
			return nil
		}
		for _, ext := range exts {
			out[ext] = true
		}
	}
	return out
}
