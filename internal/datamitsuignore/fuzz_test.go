package datamitsuignore

import (
	"testing"
)

// seedRules are representative .datamitsuignore bodies used to seed both fuzz
// targets. They cover the real-world shapes: explicit tool lists, catch-all
// wildcards, inversion, subdir scoping, comments, and malformed lines.
var seedRules = []string{
	"**/*: cspell, oxfmt, yamlfmt\n!**/.gitleaks.toml: oxfmt",
	"**/*: *",
	"**/*: *\n!**/*.md: eslint",
	"!**/*: eslint\n**/*: *",
	"# comment only\n\nvendor/**/*: prettier",
	"src/*.go: golangci-lint",
	"**/*.gen.go: golangci-lint, gofmt",
	"  !  **/*.md :  eslint , prettier  ",
	"bad line without colon",
	":",
	"!:",
	"**/*:",
}

// FuzzParse asserts that parsing arbitrary .datamitsuignore content never panics
// and never returns rules together with an error. It does not assert semantic
// correctness — that is covered by the example-based tests.
func FuzzParse(f *testing.F) {
	for _, s := range seedRules {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, content string) {
		rules, err := Parse(content)
		if err != nil && rules != nil {
			t.Fatalf("Parse returned both rules and error for %q", content)
		}
	})
}

// FuzzMatcher asserts that building a matcher from arbitrary content and querying
// it never panics and is deterministic, for both root- and subdirectory-scoped
// rule sets (the latter exercises the relative-glob scoping path). Inputs that
// fail to parse are skipped — malformed parsing is FuzzParse's concern.
func FuzzMatcher(f *testing.F) {
	f.Add("**/*: oxfmt\n!config/**/*: oxfmt", "oxfmt", "config/ci.yaml")
	f.Add("**/*: *\n!**/*.md: eslint", "eslint", "README.md")
	f.Add("src/*.go: golangci-lint", "golangci-lint", "src/main.go")
	f.Fuzz(func(t *testing.T, content, tool, path string) {
		for _, dir := range []string{"", "sub"} {
			m := NewMatcher()
			if err := m.AddFile(dir, content); err != nil {
				continue
			}
			a := m.IsDisabled(tool, path)
			b := m.IsDisabled(tool, path)
			if a != b {
				t.Fatalf("IsDisabled non-deterministic: dir=%q tool=%q path=%q -> %v then %v", dir, tool, path, a, b)
			}
			// Must not panic for the project-level probe either.
			_ = m.IsProjectDisabled(tool, path)
		}
	})
}
