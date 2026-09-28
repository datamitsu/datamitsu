package teamcity

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report/render/github"
)

// TestEscape is TeamCity's table: "|" first, then the quote, the line breaks,
// the brackets, and other control characters and the Unicode line
// separators as |0xNNNN.
func TestEscape(t *testing.T) {
	tests := map[string]string{
		"a|b":      "a||b",
		"it's":     "it|'s",
		"a\nb":     "a|nb",
		"a\rb":     "a|rb",
		"[x]":      "|[x|]",
		"a\tb":     "a|0x0009b",
		"a\u0085b": "a|0x0085b",
		"a\u2028b": "a|0x2028b",
		"a\u2029b": "a|0x2029b",
		"a\x7fb":   "a|0x007Fb",
		"é ok":     "é ok",
		"|n":       "||n",
	}
	for in, want := range tests {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeverity(t *testing.T) {
	for level, want := range map[string]string{"error": "ERROR", "warning": "WARNING", "info": "INFO", "hint": "WEAK WARNING"} {
		if got := Severity(level); got != want {
			t.Errorf("Severity(%s) = %s, want %s", level, got, want)
		}
	}
}

var javaIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func TestIdentity(t *testing.T) {
	if got := Identity("golangci_lint"); got != "golangci_lint" {
		t.Errorf("Identity keeps a Java identifier as it is, got %q", got)
	}
	a, b := Identity("golangci-lint"), Identity("golangci.lint")
	if a == b || !strings.HasPrefix(a, "golangci_lint_") {
		t.Errorf("Identity = %q, %q; want two identities, both readable", a, b)
	}
	long := Identity(strings.Repeat("x", 80))
	for _, id := range []string{a, b, long, Identity("1tool"), Identity("")} {
		if !javaIdentifier.MatchString(id) || len(id) > 60 {
			t.Errorf("Identity = %q, want at most 60 Java identifier characters", id)
		}
	}
}

func TestBuild(t *testing.T) {
	candidates := []github.Annotation{
		{Level: github.LevelWarning, File: "b.ts", Line: 2, Tool: "eslint", Source: "eslint", Code: "no-var", Severity: "warning", Message: "var", RuleURL: "https://eslint.org/docs/rules/no-var"},
		{Level: github.LevelError, File: "a.ts", Line: 1, Tool: "eslint", Source: "eslint", Code: "no-var", Severity: "error", Message: "it's [bad]"},
		{Level: github.LevelNotice, File: "a.ts", Line: 5, Tool: "eslint", Source: "eslint", Severity: "hint", Message: "hint"},
		{Level: github.LevelNotice, Tool: "eslint", Source: "eslint", Severity: "info", Message: "no file"},
		{Level: github.LevelError, Tool: "tsc", Source: "tsc", Severity: "error", Message: "tsc exited 2 without parsable findings", Synthetic: true},
	}
	var out bytes.Buffer
	if err := Print(&out, Build(candidates)); err != nil {
		t.Fatal(err)
	}
	want := "##teamcity[inspectionType id='eslint/no-var' name='no-var' description='eslint(no-var)' category='eslint']\n" +
		"##teamcity[inspectionType id='eslint/unknown' name='eslint' description='eslint' category='eslint']\n" +
		"##teamcity[inspection typeId='eslint/no-var' message='it|'s |[bad|]' file='a.ts' line='1' SEVERITY='ERROR']\n" +
		"##teamcity[inspection typeId='eslint/unknown' message='hint' file='a.ts' line='5' SEVERITY='WEAK WARNING']\n" +
		"##teamcity[inspection typeId='eslint/no-var' message='var' file='b.ts' line='2' SEVERITY='WARNING']\n" +
		"##teamcity[buildProblem description='tsc exited 2 without parsable findings' identity='tsc']\n" +
		"datamitsu: 1 finding without a file not reported as inspections\n"
	if out.String() != want {
		t.Errorf("messages =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestNeutralize(t *testing.T) {
	if got := Neutralize("x ##teamcity[enableServiceMessages] ##teamcity[buildProblem description='y']"); got !=
		"x ##teamcity [enableServiceMessages] ##teamcity [buildProblem description='y']" {
		t.Errorf("Neutralize = %q", got)
	}
}
