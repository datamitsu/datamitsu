package datamitsuignore

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantRules int
		wantErr   bool
	}{
		{
			name:      "empty content",
			content:   "",
			wantRules: 0,
		},
		{
			name:      "blank lines only",
			content:   "\n  \n\n",
			wantRules: 0,
		},
		{
			name:      "comments only",
			content:   "# comment\n# another",
			wantRules: 0,
		},
		{
			name:      "single rule",
			content:   "**/*.md: eslint, prettier",
			wantRules: 1,
		},
		{
			name:      "inversion rule",
			content:   "!docs/**/*.md: prettier",
			wantRules: 1,
		},
		{
			name:      "mixed rules and comments",
			content:   "# Disable eslint for markdown\n**/*.md: eslint\n\n# But re-enable for docs\n!docs/**/*.md: eslint\n",
			wantRules: 2,
		},
		{
			name:    "missing colon",
			content: "**/*.md eslint",
			wantErr: true,
		},
		{
			name:    "empty glob",
			content: ": eslint",
			wantErr: true,
		},
		{
			name:    "empty tool list",
			content: "**/*.md:",
			wantErr: true,
		},
		{
			name:    "only bang",
			content: "!: eslint",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, err := Parse(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rules) != tt.wantRules {
				t.Errorf("len(rules) = %d, want %d", len(rules), tt.wantRules)
			}
		})
	}
}

func TestParseRuleFields(t *testing.T) {
	t.Run("positive rule", func(t *testing.T) {
		rules, err := Parse("**/*.md: eslint, prettier")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 1 {
			t.Fatalf("len(rules) = %d, want 1", len(rules))
		}
		r := rules[0]
		if r.Glob != "**/*.md" {
			t.Errorf("Glob = %q, want %q", r.Glob, "**/*.md")
		}
		if r.Invert {
			t.Error("Invert should be false")
		}
		if len(r.Tools) != 2 {
			t.Fatalf("len(Tools) = %d, want 2", len(r.Tools))
		}
		if r.Tools[0] != "eslint" {
			t.Errorf("Tools[0] = %q, want %q", r.Tools[0], "eslint")
		}
		if r.Tools[1] != "prettier" {
			t.Errorf("Tools[1] = %q, want %q", r.Tools[1], "prettier")
		}
	})

	t.Run("inversion rule", func(t *testing.T) {
		rules, err := Parse("!docs/**/*.md: prettier")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 1 {
			t.Fatalf("len(rules) = %d, want 1", len(rules))
		}
		r := rules[0]
		if r.Glob != "docs/**/*.md" {
			t.Errorf("Glob = %q, want %q", r.Glob, "docs/**/*.md")
		}
		if !r.Invert {
			t.Error("Invert should be true")
		}
		if len(r.Tools) != 1 || r.Tools[0] != "prettier" {
			t.Errorf("Tools = %v, want [prettier]", r.Tools)
		}
	})

	t.Run("whitespace trimming", func(t *testing.T) {
		rules, err := Parse("  **/*.js  :  eslint  ,  prettier  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 1 {
			t.Fatalf("len(rules) = %d, want 1", len(rules))
		}
		r := rules[0]
		if r.Glob != "**/*.js" {
			t.Errorf("Glob = %q, want %q", r.Glob, "**/*.js")
		}
		if len(r.Tools) != 2 || r.Tools[0] != "eslint" || r.Tools[1] != "prettier" {
			t.Errorf("Tools = %v, want [eslint prettier]", r.Tools)
		}
	})

	t.Run("single tool", func(t *testing.T) {
		rules, err := Parse("*.go: golangci-lint")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 1 {
			t.Fatalf("len(rules) = %d, want 1", len(rules))
		}
		if len(rules[0].Tools) != 1 || rules[0].Tools[0] != "golangci-lint" {
			t.Errorf("Tools = %v, want [golangci-lint]", rules[0].Tools)
		}
	})
}

func TestParseRules(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		rules, err := ParseRules(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 0 {
			t.Errorf("len(rules) = %d, want 0", len(rules))
		}
	})

	t.Run("single rule line", func(t *testing.T) {
		rules, err := ParseRules([]string{"**/*.md: eslint, prettier"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 1 {
			t.Fatalf("len(rules) = %d, want 1", len(rules))
		}
		r := rules[0]
		if r.Glob != "**/*.md" {
			t.Errorf("Glob = %q, want %q", r.Glob, "**/*.md")
		}
		if len(r.Tools) != 2 || r.Tools[0] != "eslint" || r.Tools[1] != "prettier" {
			t.Errorf("Tools = %v, want [eslint prettier]", r.Tools)
		}
	})

	t.Run("multiple rules", func(t *testing.T) {
		lines := []string{
			"**/*.md: eslint",
			"vendor/**: *",
			"!docs/**/*.md: eslint",
		}
		rules, err := ParseRules(lines)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 3 {
			t.Fatalf("len(rules) = %d, want 3", len(rules))
		}
		if rules[1].Glob != "vendor/**" || rules[1].Tools[0] != "*" {
			t.Errorf("rules[1] = %+v, want glob=vendor/** tools=[*]", rules[1])
		}
		if !rules[2].Invert {
			t.Error("rules[2].Invert should be true")
		}
	})

	t.Run("blank and comment lines skipped", func(t *testing.T) {
		lines := []string{
			"",
			"# comment",
			"**/*.md: eslint",
			"  ",
			"# another comment",
		}
		rules, err := ParseRules(lines)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 1 {
			t.Errorf("len(rules) = %d, want 1", len(rules))
		}
	})

	t.Run("error on invalid line", func(t *testing.T) {
		lines := []string{"**/*.md eslint"}
		_, err := ParseRules(lines)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("consistent with Parse", func(t *testing.T) {
		content := "**/*.md: eslint\n!docs/**/*.md: prettier\n"
		parseRules, err := Parse(content)
		if err != nil {
			t.Fatalf("Parse error: %v", err)
		}
		lines := []string{"**/*.md: eslint", "!docs/**/*.md: prettier"}
		sliceRules, err := ParseRules(lines)
		if err != nil {
			t.Fatalf("ParseRules error: %v", err)
		}
		if len(parseRules) != len(sliceRules) {
			t.Fatalf("len mismatch: Parse=%d, ParseRules=%d", len(parseRules), len(sliceRules))
		}
		for i := range parseRules {
			if parseRules[i].Glob != sliceRules[i].Glob {
				t.Errorf("[%d] Glob: Parse=%q, ParseRules=%q", i, parseRules[i].Glob, sliceRules[i].Glob)
			}
			if parseRules[i].Invert != sliceRules[i].Invert {
				t.Errorf("[%d] Invert: Parse=%v, ParseRules=%v", i, parseRules[i].Invert, sliceRules[i].Invert)
			}
		}
	})
}
