package datamitsuignore

import (
	"testing"
)

func TestMatcherEmpty(t *testing.T) {
	m := NewMatcher()
	if m.IsDisabled("eslint", "src/main.js") {
		t.Error("empty matcher should not disable anything")
	}
}

func TestMatcherBasicDisable(t *testing.T) {
	m := NewMatcher()
	err := m.AddFile("", "**/*.md: eslint, prettier")
	if err != nil {
		t.Fatalf("AddFile error: %v", err)
	}

	tests := []struct {
		tool     string
		file     string
		disabled bool
	}{
		{"eslint", "README.md", true},
		{"prettier", "README.md", true},
		{"golangci-lint", "README.md", false},
		{"eslint", "docs/guide.md", true},
		{"eslint", "src/main.js", false},
		{"prettier", "src/main.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.tool+"/"+tt.file, func(t *testing.T) {
			got := m.IsDisabled(tt.tool, tt.file)
			if got != tt.disabled {
				t.Errorf("IsDisabled(%q, %q) = %v, want %v", tt.tool, tt.file, got, tt.disabled)
			}
		})
	}
}

func TestMatcherInversion(t *testing.T) {
	m := NewMatcher()
	// Root disables eslint for all markdown
	err := m.AddFile("", "**/*.md: eslint")
	if err != nil {
		t.Fatalf("AddFile root error: %v", err)
	}
	// docs/ re-enables eslint for its own markdown
	err = m.AddFile("docs", "!**/*.md: eslint")
	if err != nil {
		t.Fatalf("AddFile docs error: %v", err)
	}

	tests := []struct {
		tool     string
		file     string
		disabled bool
	}{
		{"eslint", "README.md", true},
		{"eslint", "src/notes.md", true},
		{"eslint", "docs/guide.md", false},
		{"eslint", "docs/api/ref.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := m.IsDisabled(tt.tool, tt.file)
			if got != tt.disabled {
				t.Errorf("IsDisabled(%q, %q) = %v, want %v", tt.tool, tt.file, got, tt.disabled)
			}
		})
	}
}

func TestMatcherParentOverrideChildReEnable(t *testing.T) {
	m := NewMatcher()
	// Root disables prettier for everything
	if err := m.AddFile("", "**/*: prettier"); err != nil {
		t.Fatal(err)
	}
	// src/ re-enables prettier
	if err := m.AddFile("src", "!**/*: prettier"); err != nil {
		t.Fatal(err)
	}

	if !m.IsDisabled("prettier", "README.md") {
		t.Error("root README.md: prettier should be disabled")
	}
	if m.IsDisabled("prettier", "src/main.js") {
		t.Error("src/main.js: prettier should be re-enabled")
	}
	if m.IsDisabled("prettier", "src/lib/util.js") {
		t.Error("src/lib/util.js: prettier should be re-enabled by src/ rule")
	}
}

func TestMatcherMultipleTools(t *testing.T) {
	m := NewMatcher()
	if err := m.AddFile("", "**/*.gen.go: golangci-lint, gofmt"); err != nil {
		t.Fatal(err)
	}

	if !m.IsDisabled("golangci-lint", "internal/gen.gen.go") {
		t.Error("golangci-lint should be disabled for .gen.go")
	}
	if !m.IsDisabled("gofmt", "internal/gen.gen.go") {
		t.Error("gofmt should be disabled for .gen.go")
	}
	if m.IsDisabled("govet", "internal/gen.gen.go") {
		t.Error("govet should not be disabled")
	}
}

func TestMatcherScopedGlobs(t *testing.T) {
	m := NewMatcher()
	// A rule in a subdirectory with a non-** glob should be scoped to that dir
	if err := m.AddFile("vendor", "*.go: golangci-lint"); err != nil {
		t.Fatal(err)
	}

	if !m.IsDisabled("golangci-lint", "vendor/lib.go") {
		t.Error("vendor/lib.go: golangci-lint should be disabled")
	}
	if m.IsDisabled("golangci-lint", "src/main.go") {
		t.Error("src/main.go: golangci-lint should not be disabled by vendor rule")
	}
}

func TestMatcherAddFileError(t *testing.T) {
	m := NewMatcher()
	err := m.AddFile("", "bad line without colon")
	if err == nil {
		t.Fatal("expected error for invalid content")
	}
}

func TestMatcherMultipleRulesInOneFile(t *testing.T) {
	m := NewMatcher()
	content := `# Generated files
**/*.gen.go: golangci-lint

# Vendor
vendor/**/*: eslint, prettier

# But keep prettier for vendor docs
!vendor/docs/**/*: prettier
`
	if err := m.AddFile("", content); err != nil {
		t.Fatal(err)
	}

	if !m.IsDisabled("golangci-lint", "pkg/types.gen.go") {
		t.Error("golangci-lint should be disabled for .gen.go")
	}
	if m.IsDisabled("golangci-lint", "pkg/types.go") {
		t.Error("golangci-lint should not be disabled for regular .go")
	}
	if !m.IsDisabled("eslint", "vendor/lib/index.js") {
		t.Error("eslint should be disabled in vendor")
	}
	if !m.IsDisabled("prettier", "vendor/lib/index.js") {
		t.Error("prettier should be disabled in vendor (not in vendor/docs)")
	}
	if m.IsDisabled("prettier", "vendor/docs/readme.md") {
		t.Error("prettier should be re-enabled in vendor/docs")
	}
}

func TestMatcherNoRulesForDir(t *testing.T) {
	m := NewMatcher()
	if err := m.AddFile("other", "**/*.js: eslint"); err != nil {
		t.Fatal(err)
	}
	if m.IsDisabled("eslint", "src/main.js") {
		t.Error("rule in 'other' should not affect 'src'")
	}
}

func TestMatcherWildcardDisablesAllTools(t *testing.T) {
	m := NewMatcher()
	if err := m.AddFile("", "vendor/**/*: *"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		tool     string
		file     string
		disabled bool
	}{
		{"eslint", "vendor/lib.js", true},
		{"prettier", "vendor/lib.js", true},
		{"golangci-lint", "vendor/main.go", true},
		{"eslint", "src/main.js", false},
		{"prettier", "src/main.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.tool+"/"+tt.file, func(t *testing.T) {
			got := m.IsDisabled(tt.tool, tt.file)
			if got != tt.disabled {
				t.Errorf("IsDisabled(%q, %q) = %v, want %v", tt.tool, tt.file, got, tt.disabled)
			}
		})
	}
}

func TestMatcherWildcardInversion(t *testing.T) {
	m := NewMatcher()
	// Root disables all tools for vendor
	if err := m.AddFile("", "vendor/**/*: *"); err != nil {
		t.Fatal(err)
	}
	// vendor/docs re-enables all tools
	if err := m.AddFile("vendor/docs", "!**/*: *"); err != nil {
		t.Fatal(err)
	}

	if !m.IsDisabled("eslint", "vendor/lib.js") {
		t.Error("eslint should be disabled in vendor")
	}
	if !m.IsDisabled("prettier", "vendor/lib.js") {
		t.Error("prettier should be disabled in vendor")
	}
	if m.IsDisabled("eslint", "vendor/docs/readme.md") {
		t.Error("eslint should be re-enabled in vendor/docs")
	}
	if m.IsDisabled("prettier", "vendor/docs/readme.md") {
		t.Error("prettier should be re-enabled in vendor/docs")
	}
}

func TestMatcherWildcardInversionClearsSpecific(t *testing.T) {
	m := NewMatcher()
	// Root disables specific tools
	if err := m.AddFile("", "**/*: eslint, prettier"); err != nil {
		t.Fatal(err)
	}
	// Subdirectory re-enables everything via wildcard inversion
	if err := m.AddFile("src", "!**/*: *"); err != nil {
		t.Fatal(err)
	}

	if !m.IsDisabled("eslint", "README.md") {
		t.Error("eslint should be disabled at root")
	}
	if m.IsDisabled("eslint", "src/main.js") {
		t.Error("eslint should be re-enabled in src via wildcard inversion")
	}
	if m.IsDisabled("prettier", "src/main.js") {
		t.Error("prettier should be re-enabled in src via wildcard inversion")
	}
}

func TestIsProjectDisabledBasic(t *testing.T) {
	m := NewMatcher()
	if err := m.AddFile("", "vendor/**/*: eslint"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		tool       string
		projectDir string
		disabled   bool
	}{
		{"eslint", "vendor", true},
		{"eslint", "vendor/sub", true},
		{"eslint", "src", false},
		{"prettier", "vendor", false},
	}

	for _, tt := range tests {
		t.Run(tt.tool+"/"+tt.projectDir, func(t *testing.T) {
			got := m.IsProjectDisabled(tt.tool, tt.projectDir)
			if got != tt.disabled {
				t.Errorf("IsProjectDisabled(%q, %q) = %v, want %v", tt.tool, tt.projectDir, got, tt.disabled)
			}
		})
	}
}

func TestIsProjectDisabledInversion(t *testing.T) {
	m := NewMatcher()
	// Disable eslint for all vendor
	if err := m.AddFile("", "vendor/**/*: eslint"); err != nil {
		t.Fatal(err)
	}
	// Re-enable eslint for vendor/docs
	if err := m.AddFile("vendor/docs", "!**/*: eslint"); err != nil {
		t.Fatal(err)
	}

	if !m.IsProjectDisabled("eslint", "vendor") {
		t.Error("eslint should be disabled for vendor project")
	}
	if !m.IsProjectDisabled("eslint", "vendor/lib") {
		t.Error("eslint should be disabled for vendor/lib project")
	}
	if m.IsProjectDisabled("eslint", "vendor/docs") {
		t.Error("eslint should be re-enabled for vendor/docs project")
	}
	if m.IsProjectDisabled("eslint", "vendor/docs/api") {
		t.Error("eslint should be re-enabled for vendor/docs/api project")
	}
}

func TestIsProjectDisabledExtensionRulesDoNotDisableProject(t *testing.T) {
	m := NewMatcher()
	// Extension-specific rules should NOT disable an entire project
	if err := m.AddFile("", "**/*.md: eslint"); err != nil {
		t.Fatal(err)
	}

	if m.IsProjectDisabled("eslint", "src") {
		t.Error("extension-specific rule should not disable entire project")
	}
	if m.IsProjectDisabled("eslint", "") {
		t.Error("extension-specific rule should not disable root project")
	}
}

func TestIsProjectDisabledWildcard(t *testing.T) {
	m := NewMatcher()
	if err := m.AddFile("", "vendor/**/*: *"); err != nil {
		t.Fatal(err)
	}

	if !m.IsProjectDisabled("eslint", "vendor") {
		t.Error("eslint should be disabled for vendor via wildcard")
	}
	if !m.IsProjectDisabled("prettier", "vendor") {
		t.Error("prettier should be disabled for vendor via wildcard")
	}
	if m.IsProjectDisabled("eslint", "src") {
		t.Error("eslint should not be disabled for src")
	}
}

func TestIsProjectDisabledEmpty(t *testing.T) {
	m := NewMatcher()
	if m.IsProjectDisabled("eslint", "src") {
		t.Error("empty matcher should not disable anything")
	}
}

func TestMatcherSameDepthInsertionOrder(t *testing.T) {
	// Config-defined rules (added first) disable eslint for *.md.
	// File-based rules (added second) re-enable eslint for *.md via inversion.
	// With stable sort, insertion order is preserved: config rules apply first,
	// then file rules override. eslint should be enabled (inversion wins).
	m := NewMatcher()
	if err := m.AddFile("", "**/*.md: eslint"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddFile("", "!**/*.md: eslint"); err != nil {
		t.Fatal(err)
	}

	if m.IsDisabled("eslint", "docs/README.md") {
		t.Error("eslint should NOT be disabled: second root entry (inversion) should override first")
	}
}
