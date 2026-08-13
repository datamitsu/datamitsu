package runner

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestFormatDiagnostic_FullAndMinimal(t *testing.T) {
	// Color codes (if any) wrap the tokens, so substring checks hold either way.
	full := formatDiagnostic(diagnostic.Diagnostic{
		File: "f.js", Row: 1, Col: 5,
		Severity: diagnostic.SeverityError, Message: "bad thing", Code: "no-undef",
	})
	for _, want := range []string{"f.js:1:5", "error", "bad thing", "[no-undef]"} {
		if !strings.Contains(full, want) {
			t.Errorf("formatDiagnostic = %q, missing %q", full, want)
		}
	}

	// No file → just row:col; no code → no brackets.
	minimal := formatDiagnostic(diagnostic.Diagnostic{
		Row: 3, Col: 2, Severity: diagnostic.SeverityWarning, Message: "m",
	})
	if !strings.Contains(minimal, "3:2") || !strings.Contains(minimal, "warning") {
		t.Errorf("minimal = %q, want 3:2 + warning", minimal)
	}
	if strings.Contains(minimal, "[") {
		t.Errorf("no code should mean no brackets: %q", minimal)
	}
}

// relativeToBase is asserted directly rather than through the formatted line:
// a substring check on the rendered text passes even when the path was left
// untouched ("…/repo/pkg/cli/src/a.ts" contains "src/a.ts"), so it would not
// catch the function returning its input.
func TestRelativeToBase(t *testing.T) {
	cases := []struct {
		name, file, base, want string
	}{
		{"under the base is shortened", "/repo/pkg/cli/src/a.ts", "/repo/pkg/cli", "src/a.ts"},
		{"the base itself", "/repo/a.ts", "/repo", "a.ts"},
		// Escaping upwards reads worse than the absolute path it came from.
		{"sibling of the base", "/repo/pkg/other/a.ts", "/repo/pkg/cli", "/repo/pkg/other/a.ts"},
		{"unrelated base", "/repo/pkg/cli/src/a.ts", "/somewhere/else", "/repo/pkg/cli/src/a.ts"},
		{"no base", "/repo/pkg/cli/src/a.ts", "", "/repo/pkg/cli/src/a.ts"},
		// Already relative: the parser reported it against the tool's own cwd.
		{"relative input", "src/b.ts", "/repo", "src/b.ts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := relativeToBase(c.file, c.base); got != c.want {
				t.Errorf("relativeToBase(%q, %q) = %q, want %q", c.file, c.base, got, c.want)
			}
		})
	}
}

func TestFormatDiagnosticRelativeTo(t *testing.T) {
	d := diagnostic.Diagnostic{
		File: "/repo/pkg/cli/src/a.ts", Row: 9, Col: 1,
		Severity: diagnostic.SeverityError, Message: "m",
	}

	got := formatDiagnosticRelativeTo(d, "/repo/pkg/cli")
	if !strings.Contains(got, "src/a.ts:9:1") {
		t.Errorf("path not shortened against the cwd: %q", got)
	}
	if strings.Contains(got, "/repo/pkg/cli/src/a.ts") {
		t.Errorf("the absolute path survived shortening: %q", got)
	}
}

// TestUsableDiagnostics covers the guard that keeps a batch tool whose parser
// reports no paths from replacing the raw output — which does name the files —
// with an unattributable list.
func TestUsableDiagnostics(t *testing.T) {
	withFile := diagnostic.Diagnostic{File: "a.md", Message: "m"}
	noFile := diagnostic.Diagnostic{Message: "m"}

	cases := []struct {
		name  string
		batch bool
		diags []diagnostic.Diagnostic
		want  bool
	}{
		{"none parsed", true, nil, false},
		{"batch, no file on any", true, []diagnostic.Diagnostic{noFile, noFile}, false},
		{"batch, one file is enough", true, []diagnostic.Diagnostic{noFile, withFile}, true},
		// Per-file runs read fine unattributed: the box names the one file linted.
		{"per-file, no file", false, []diagnostic.Diagnostic{noFile}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := tooling.ExecutionResult{Batch: c.batch, Diagnostics: c.diags}
			if got := usableDiagnostics(result); got != c.want {
				t.Errorf("usableDiagnostics() = %v, want %v", got, c.want)
			}
		})
	}
}
