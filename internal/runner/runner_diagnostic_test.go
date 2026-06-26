package runner

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
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
