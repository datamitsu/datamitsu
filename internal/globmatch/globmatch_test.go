package globmatch

import (
	"reflect"
	"sort"
	"testing"

	"github.com/bmatcuk/doublestar/v4"
)

// corpus is the set of path shapes a repository walk actually produces: nested
// and root-level, dotfiles, multi-dot names, a name whose longer extension
// contains a shorter one, a directory whose name carries the extension, spaces,
// and non-ASCII.
var corpus = []string{
	"a.ts",
	".ts",
	"x.tsx",
	"a.d.ts",
	"a.config.js",
	"a.js",
	"src/a.ts",
	"src/nested/deep/a.ts",
	"src/.hidden.ts",
	".github/workflows/ci.yaml",
	"packages/ui/src/index.tsx",
	"dir.ts/file.js",
	"dir.ts/file.ts",
	"weird.ts.bak",
	"README.md",
	"Makefile",
	"a b/c d.ts",
	// A multi-byte UTF-8 path, spelled with escapes so it stays test data
	// rather than becoming a dictionary entry.
	"dir\u00fc/file\u00ef.ts",
	"a/.datamitsuignore",
	".datamitsuignore",
	"vendor/lib.go",
	"go.mod",
	"apps/web/package.json",
	"package.json",
	"package.json" + "x", // a near-miss for **/package.json
	"apps/package.json/nested.ts",
	"Cargo.toml",
	"deep/a/b/c/d/e/go.mod",
}

// patterns covers every shape prepare() classifies, fast paths and fallbacks
// alike. Each is checked against doublestar over the whole corpus, so a fast
// path that disagrees with the real matcher fails here rather than silently
// planning the wrong file set.
var patterns = []string{
	// suffix fast path
	"**/*.ts", "**/*.tsx", "**/*.js", "**/*.d.ts", "**/*.config.js",
	"**/*.yaml", "**/*.md", "**/*.go", "**/*.json",
	// final-segment fast path
	"**/package.json", "**/go.mod", "**/Makefile", "**/.datamitsuignore",
	"**/Cargo.toml",
	// literal fast path
	"go.mod", "package.json", "Makefile", "README.md",
	// doublestar fallbacks
	"**/*", "**/*.{ts,tsx}", "**/*.[jt]s", "**/*.*", "src/**/*.ts",
	"*.ts", "**/test/*.ts", "**/*.ts?", "packages/*/src/*.ts",
	"**/a/b", "apps/**/package.json", "**/src/**",
}

func TestFastPathsMatchDoublestar(t *testing.T) {
	for _, p := range patterns {
		set := New([]string{p})
		for _, path := range corpus {
			want, err := doublestar.Match(p, path)
			if err != nil {
				t.Fatalf("doublestar.Match(%q, %q): %v", p, path, err)
			}
			if got := set.Match(path); got != want {
				t.Errorf("pattern %q (kind %d) path %q: globmatch = %v, doublestar = %v",
					p, set.patterns[0].kind, path, got, want)
			}
		}
	}
}

func TestSetMatchesDoublestarForMixedLists(t *testing.T) {
	lists := [][]string{
		{"**/*.ts", "**/*.tsx", "**/*.js"},
		{"**/*.{ts,tsx}", "**/*.md"},
		{"src/**/*.ts", "**/*.yaml"},
		{"**/*", "**/*.ts"},
		{"Makefile", "**/*.go"},
		{"**/*.d.ts", "**/*.ts"},
		{"**/package.json", "**/go.mod", "**/Cargo.toml"},
		{},
	}

	for _, globs := range lists {
		set := New(globs)
		for _, path := range corpus {
			want := false
			for _, p := range globs {
				matched, err := doublestar.Match(p, path)
				if err != nil {
					t.Fatalf("doublestar.Match(%q, %q): %v", p, path, err)
				}
				if matched {
					want = true
					break
				}
			}
			if got := set.Match(path); got != want {
				t.Errorf("globs %v path %q: globmatch = %v, doublestar = %v", globs, path, got, want)
			}
		}
	}
}

// TestPrepareKinds pins which shape each pattern is classified as. It is the
// test that fails when a refactor quietly stops taking a fast path — the
// correctness tests above would still pass, and the regression would be
// invisible.
func TestPrepareKinds(t *testing.T) {
	cases := map[string]kind{
		"**/*.ts":             kindSuffix,
		"**/*.config.js":      kindSuffix,
		"**/package.json":     kindSegment,
		"**/.datamitsuignore": kindSegment,
		"go.mod":              kindLiteral,
		"README.md":           kindLiteral,
		"**/*":                kindDoublestar,
		"**/*.{ts,tsx}":       kindDoublestar,
		"**/a/b":              kindDoublestar,
		"src/**/*.ts":         kindDoublestar,
		"*.ts":                kindDoublestar,
		"**/*.[jt]s":          kindDoublestar,
	}

	for glob, want := range cases {
		if got := prepare(glob).kind; got != want {
			t.Errorf("prepare(%q).kind = %d, want %d", glob, got, want)
		}
	}
}

func TestExtensions(t *testing.T) {
	cases := []struct {
		glob string
		want []string
	}{
		{"**/*.go", []string{".go"}},
		{"*.go", []string{".go"}},
		{"**/*.{ts,tsx,js}", []string{".ts", ".tsx", ".js"}},
		{"**/*.config.js", []string{".config.js"}},
		{"**/*", nil},
		{"**/*.*", nil},
		{"Makefile", nil},
		{"**/package.json", nil},
		{"**/*.{}", nil},
		{"**/*.{ts,*}", nil},
		{"src/**", nil},
		{"*.", nil},
		{"*.{ts,}", nil},
		{"*.g*", nil},
		{"*.[ab]", nil},
	}

	for _, c := range cases {
		got := Extensions(c.glob)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Extensions(%q) = %v, want %v", c.glob, got, c.want)
		}
	}
}

func TestExtensionsAll(t *testing.T) {
	got := ExtensionsAll([]string{"**/*.ts", "**/*.{js,jsx}"})
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{".js", ".jsx", ".ts"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("ExtensionsAll = %v, want %v", keys, want)
	}

	if got := ExtensionsAll([]string{"**/*.ts", "Makefile"}); got != nil {
		t.Errorf("ExtensionsAll with an irreducible pattern = %v, want nil", got)
	}
}
