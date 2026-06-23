package clitest

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// updateGolden, when set via `go test -update`, makes AssertGolden rewrite the
// golden files instead of comparing against them. It is registered once at
// package init; the test binary's flag.Parse (run by testing.Main) populates it.
var updateGolden = flag.Bool("update", false, "rewrite golden files instead of comparing against them")

// GoldenDir is the directory, relative to the calling test's working directory
// (its package dir), where AssertGolden reads and writes golden files. The Go
// convention is per-package testdata, so each test package gets its own goldens.
var GoldenDir = filepath.Join("testdata", "golden")

// Precompiled normalization patterns. Keeping them package-level means every
// golden shares identical rules and they compile exactly once.
var (
	// ansiRE matches CSI escape sequences (colors, cursor moves) so normalized
	// output is plain text regardless of terminal detection quirks.
	ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
	// timestampRE matches ISO-8601-ish timestamps → <TS>.
	timestampRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	// durationRE matches Go-formatted durations such as 1.5s, 1m30s, 500ms,
	// 1.2µs, 3h0m0s → <DUR>. It matches one or more number+unit components so a
	// compound duration collapses to a single placeholder.
	durationRE = regexp.MustCompile(`\d+(\.\d+)?(ns|µs|us|ms|s|m|h)(\d+(\.\d+)?(ns|µs|us|ms|s|m|h))*`)
	// ruleRE matches runs of box-drawing horizontal rule characters (banner
	// borders and the init/setup phase header/footer fills) → <RULE>. These fills
	// pad a line to the detected width, so their length depends on the width of
	// content that is itself masked away (version, duration), which would make
	// goldens flaky run-to-run and build-to-build. A run of 3+ never appears in
	// semantic text, while the single bar in "┏━"/"┗━"/"┃" prefixes is preserved.
	ruleRE = regexp.MustCompile(`[━─]{3,}`)
)

// Normalizer rewrites subprocess output into a stable, machine-independent form
// for golden comparison. Built-in rules (ANSI strip, timestamps, durations, the
// build version) always apply; callers register environment-specific path masks
// with MaskPath. The zero value is not usable — construct via NewNormalizer.
type Normalizer struct {
	paths     []pathMask
	sortLines bool
}

// pathMask replaces every occurrence of an absolute path with a placeholder.
type pathMask struct {
	path        string
	placeholder string
}

// NewNormalizer returns a Normalizer with the built-in rules enabled and no
// path masks. Chain MaskPath / SortLines to configure it.
func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

// MaskPath registers an absolute path to replace with placeholder (e.g. the
// temp project dir → "<TMP>"). Registration order does not matter: Apply masks
// the longest paths first so a cache dir nested under $HOME is masked before
// $HOME itself. It returns the receiver for chaining.
func (n *Normalizer) MaskPath(path, placeholder string) *Normalizer {
	if path != "" {
		n.paths = append(n.paths, pathMask{path: path, placeholder: placeholder})
	}
	return n
}

// SortLines makes Apply sort output lines lexicographically, for commands whose
// line order is not contractually guaranteed. It returns the receiver.
func (n *Normalizer) SortLines() *Normalizer {
	n.sortLines = true
	return n
}

// Apply runs every normalization rule, in a fixed order, and returns the stable
// form. Apply is idempotent: normalizing already-normalized text is a no-op.
func (n *Normalizer) Apply(s string) string {
	// 1. Strip ANSI so later text rules see plain characters.
	s = ansiRE.ReplaceAllString(s, "")

	// 2. Mask paths, longest first, so nested paths win over their parents.
	masks := make([]pathMask, len(n.paths))
	copy(masks, n.paths)
	sort.SliceStable(masks, func(i, j int) bool {
		return len(masks[i].path) > len(masks[j].path)
	})
	for _, m := range masks {
		s = strings.ReplaceAll(s, m.path, m.placeholder)
	}

	// 3. Timestamps before durations (a timestamp carries no duration unit, so
	// the two never overlap, but fixing the order keeps the rules independent).
	s = timestampRE.ReplaceAllString(s, "<TS>")

	// 4. The build version (e.g. "dev" locally, "0.1.6" on a release build).
	if v := ldflags.Version; v != "" {
		versionRE := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\b`)
		s = versionRE.ReplaceAllString(s, "<VERSION>")
	}

	// 5. Durations.
	s = durationRE.ReplaceAllString(s, "<DUR>")

	// 6. Box-drawing rule fills (width-dependent padding) → a single placeholder.
	s = ruleRE.ReplaceAllString(s, "<RULE>")

	if n.sortLines {
		s = sortLines(s)
	}
	return s
}

// sortLines sorts the lines of s lexicographically, preserving a single
// trailing newline if present so the result stays a clean text block.
func sortLines(s string) string {
	trailing := strings.HasSuffix(s, "\n")
	body := strings.TrimSuffix(s, "\n")
	if body == "" {
		return s
	}
	lines := strings.Split(body, "\n")
	sort.Strings(lines)
	out := strings.Join(lines, "\n")
	if trailing {
		out += "\n"
	}
	return out
}

// AssertGolden compares got against the golden file testdata/golden/<name>.txt
// (relative to the calling package). With `-update`, it rewrites the file
// instead and passes. On mismatch it fails the test with a readable line diff.
// got should already be normalized (see Normalizer).
func AssertGolden(tb testing.TB, name, got string) {
	tb.Helper()
	compareGolden(tb, GoldenDir, name, got, *updateGolden)
}

// compareGolden is the testable core of AssertGolden with the golden directory
// and update flag passed explicitly, so the harness's own tests can drive it
// against a temp dir without touching real goldens or the global flag.
func compareGolden(tb testing.TB, dir, name, got string, update bool) {
	tb.Helper()
	path := filepath.Join(dir, name+".txt")

	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("clitest: mkdir golden dir %s: %v", dir, err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			tb.Fatalf("clitest: write golden %s: %v", path, err)
		}
		return
	}

	wantBytes, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("clitest: read golden %s (run with -update to create it): %v", path, err)
	}
	if want := string(wantBytes); got != want {
		tb.Fatalf("clitest: golden %q mismatch (run with -update to accept):\n%s", name, lineDiff(want, got))
	}
}

// lineDiff renders a compact, readable line-by-line comparison of want vs got:
// "  " for equal lines, "- " for want-only, "+ " for got-only.
func lineDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	var b strings.Builder
	b.WriteString("--- want\n+++ got\n")
	n := max(len(wantLines), len(gotLines))
	for i := range n {
		var w, g string
		haveW, haveG := i < len(wantLines), i < len(gotLines)
		if haveW {
			w = wantLines[i]
		}
		if haveG {
			g = gotLines[i]
		}
		switch {
		case haveW && haveG && w == g:
			b.WriteString("  " + w + "\n")
		default:
			if haveW {
				b.WriteString("- " + w + "\n")
			}
			if haveG {
				b.WriteString("+ " + g + "\n")
			}
		}
	}
	return b.String()
}
