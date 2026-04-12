package bundled

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindIgnoreFiles(t *testing.T) {
	root := t.TempDir()

	dirs := []string{
		"subdir",
		"subdir/nested",
		".git",
		".git/refs",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ignoreFiles := []string{
		".datamitsuignore",
		filepath.Join("subdir", ".datamitsuignore"),
		filepath.Join("subdir", "nested", ".datamitsuignore"),
	}
	gitIgnoreFile := filepath.Join(".git", ".datamitsuignore")

	for _, f := range ignoreFiles {
		writeTestFile(t, filepath.Join(root, f), "*.ts: eslint")
	}
	writeTestFile(t, filepath.Join(root, gitIgnoreFile), "*.ts: eslint")

	found, err := FindIgnoreFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(found) != len(ignoreFiles) {
		t.Fatalf("expected %d files, got %d: %v", len(ignoreFiles), len(found), found)
	}

	for _, f := range found {
		rel, _ := filepath.Rel(root, f)
		if rel == gitIgnoreFile || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			t.Errorf("should not include files under .git: %s", f)
		}
	}
}

func TestFindIgnoreFilesEmpty(t *testing.T) {
	root := t.TempDir()
	found, err := FindIgnoreFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("expected 0 files, got %d", len(found))
	}
}

func TestRunFixNormalizesFormatting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "**/*.ts:  eslint , prettier\n! vendor/** :  tool1,tool2\n")

	if err := RunFix(root); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	expected := "**/*.ts: eslint, prettier\n!vendor/**: tool1, tool2\n"
	if string(got) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, string(got))
	}
}

func TestRunFixPreservesCommentsAndBlanks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	content := "# This is a comment\n\n*.go: golangci-lint\n\n# Another comment\n"
	writeTestFile(t, path, content)

	if err := RunFix(root); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != content {
		t.Errorf("content should be unchanged.\nexpected:\n%s\ngot:\n%s", content, string(got))
	}
}

func TestRunFixNoWriteWhenAlreadyNormalized(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	content := "*.go: golangci-lint, gofmt\n"
	writeTestFile(t, path, content)

	info1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := RunFix(root); err != nil {
		t.Fatal(err)
	}

	info2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("content changed unexpectedly")
	}

	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("file was rewritten even though content was already normalized")
	}
}

func TestRunFixParseErrorFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "invalid line without colon\n")

	err := RunFix(root)
	if err == nil {
		t.Fatal("expected error for parse failure, got nil")
	}
}

func TestRunLintParseErrorFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "invalid line without colon\n")

	tools := config.MapOfTools{}
	err := RunLint(root, tools)
	if err == nil {
		t.Fatal("expected error for parse failure, got nil")
	}
}

func TestRunLintUnknownToolWarning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "*.go: known-tool, unknown-tool\n")

	tools := config.MapOfTools{
		"known-tool": config.Tool{Name: "known-tool"},
	}

	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for unknown tool warning, got: %v", err)
	}
}

func TestRunLintWildcardNotWarned(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "vendor/**: *\n")

	tools := config.MapOfTools{}

	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for wildcard tool, got: %v", err)
	}
}

func TestRunLintValidFileNoError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "*.go: golangci-lint\nvendor/**: *\n")

	tools := config.MapOfTools{
		"golangci-lint": config.Tool{Name: "golangci-lint"},
	}

	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for valid file, got: %v", err)
	}
}

func TestRunLintFormattingWarning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "*.go:  golangci-lint ,  gofmt\n")

	tools := config.MapOfTools{
		"golangci-lint": config.Tool{Name: "golangci-lint"},
		"gofmt":         config.Tool{Name: "gofmt"},
	}

	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for formatting warning, got: %v", err)
	}
}

func TestRunFixMultipleFiles(t *testing.T) {
	root := t.TempDir()

	sub := filepath.Join(root, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	writeTestFile(t, filepath.Join(root, ".datamitsuignore"), "*.ts:  eslint\n")
	writeTestFile(t, filepath.Join(sub, ".datamitsuignore"), "*.go:golangci-lint\n")

	if err := RunFix(root); err != nil {
		t.Fatal(err)
	}

	got1, err := os.ReadFile(filepath.Join(root, ".datamitsuignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got1) != "*.ts: eslint\n" {
		t.Errorf("root file not normalized: %s", got1)
	}

	got2, err := os.ReadFile(filepath.Join(sub, ".datamitsuignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != "*.go: golangci-lint\n" {
		t.Errorf("subdir file not normalized: %s", got2)
	}
}

func TestNormalizeRule(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"**/*.ts:  eslint , prettier", "**/*.ts: eslint, prettier"},
		{"! vendor/** :  tool1,tool2", "!vendor/**: tool1, tool2"},
		{"*.go: golangci-lint", "*.go: golangci-lint"},
		{"!*.md: markdownlint", "!*.md: markdownlint"},
	}

	for _, tc := range tests {
		got, err := normalizeLine(tc.input)
		if err != nil {
			t.Errorf("normalizeLine(%q) error: %v", tc.input, err)
			continue
		}
		if got != tc.expected {
			t.Errorf("normalizeLine(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestNormalizeLinePreservesNonRules(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"# comment", "# comment"},
		{"  # indented comment", "  # indented comment"},
		{"   ", ""},
		{"\t\t", ""},
	}

	for _, tc := range tests {
		got, err := normalizeLine(tc.input)
		if err != nil {
			t.Errorf("normalizeLine(%q) error: %v", tc.input, err)
			continue
		}
		if got != tc.expected {
			t.Errorf("normalizeLine(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestRunLintEmptyDir(t *testing.T) {
	root := t.TempDir()
	tools := config.MapOfTools{}
	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for empty dir, got: %v", err)
	}
}

func TestRunFixEmptyDir(t *testing.T) {
	root := t.TempDir()
	err := RunFix(root)
	if err != nil {
		t.Fatalf("expected no error for empty dir, got: %v", err)
	}
}

func TestRunFixRemovesLeadingBlankLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "\n\n*.go: golangci-lint\n")

	if err := RunFix(root); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "*.go: golangci-lint\n" {
		t.Errorf("expected leading blank lines removed, got: %q", string(got))
	}
}

func TestRunFixCollapsesConsecutiveBlankLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "*.go: golangci-lint\n\n\n\n*.ts: eslint\n")

	if err := RunFix(root); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "*.go: golangci-lint\n\n*.ts: eslint\n" {
		t.Errorf("expected consecutive blank lines collapsed, got: %q", string(got))
	}
}

func TestRunFixConvertsWhitespaceOnlyLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "*.go: golangci-lint\n   \n*.ts: eslint\n")

	if err := RunFix(root); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "*.go: golangci-lint\n\n*.ts: eslint\n" {
		t.Errorf("expected whitespace-only line converted to empty, got: %q", string(got))
	}
}

func TestRunLintLeadingBlankLineWarning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "\n*.go: golangci-lint\n")

	tools := config.MapOfTools{"golangci-lint": config.Tool{Name: "golangci-lint"}}
	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for leading blank line (only warning), got: %v", err)
	}
}

func TestRunLintConsecutiveBlankLinesWarning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "*.go: golangci-lint\n\n\n*.ts: eslint\n")

	tools := config.MapOfTools{
		"golangci-lint": config.Tool{Name: "golangci-lint"},
		"eslint":        config.Tool{Name: "eslint"},
	}
	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for consecutive blank lines (only warning), got: %v", err)
	}
}

func TestRunLintWhitespaceOnlyLineWarning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".datamitsuignore")
	writeTestFile(t, path, "*.go: golangci-lint\n   \n*.ts: eslint\n")

	tools := config.MapOfTools{
		"golangci-lint": config.Tool{Name: "golangci-lint"},
		"eslint":        config.Tool{Name: "eslint"},
	}
	err := RunLint(root, tools)
	if err != nil {
		t.Fatalf("expected no error for whitespace-only line (only warning), got: %v", err)
	}
}

func TestNormalizeFileStructure(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "removes leading empty lines",
			input:    []string{"", "", "rule", ""},
			expected: []string{"rule", ""},
		},
		{
			name:     "collapses consecutive empty lines",
			input:    []string{"rule1", "", "", "", "rule2", ""},
			expected: []string{"rule1", "", "rule2", ""},
		},
		{
			name:     "single blank between rules preserved",
			input:    []string{"rule1", "", "rule2", ""},
			expected: []string{"rule1", "", "rule2", ""},
		},
		{
			name:     "no trailing newline preserved",
			input:    []string{"rule1", "", "", "rule2"},
			expected: []string{"rule1", "", "rule2"},
		},
		{
			name:     "empty input",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "only trailing newline",
			input:    []string{""},
			expected: []string{""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeFileStructure(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("len mismatch: got %v, want %v", got, tc.expected)
			}
			for i := range tc.expected {
				if got[i] != tc.expected[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}
