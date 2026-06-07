package runtimemanager

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/ui"
)

// TestUVReporterPhaseLines feeds uv's per-phase stderr lines (captured from uv
// 0.11) and asserts the parsed resolved/prepared counts; non-matching lines are
// ignored. A nil *ui.Spinner is safe (SetLabel guards a nil receiver).
func TestUVReporterPhaseLines(t *testing.T) {
	var sp *ui.Spinner
	rep := &uvReporter{sp: sp}

	for _, l := range []string{
		"Using CPython 3.12.3 interpreter at: /usr/bin/python3",
		"Creating virtual environment at: .venv",
		"Resolved 4 packages in 729ms",
		"Prepared 3 packages in 546ms",
		"Installed 3 packages in 4ms",
		" + something==1.0.0",
		"some unrelated line",
	} {
		rep.line(l)
	}

	if rep.resolved != 4 {
		t.Errorf("resolved = %d, want 4", rep.resolved)
	}
	if rep.prepared != 3 {
		t.Errorf("prepared = %d, want 3", rep.prepared)
	}
}

// TestUVInstalledCount verifies the installed-package count is read from uv's
// --output-format=json summary, and that missing/invalid input yields 0.
func TestUVInstalledCount(t *testing.T) {
	jsonOut := `{
	  "schema": {"version": "preview"},
	  "sync": {
	    "action": "create",
	    "changes": [
	      {"name": "one", "version": "1.0", "action": "installed"},
	      {"name": "two", "version": "2.0", "action": "installed"},
	      {"name": "three", "version": "3.0", "action": "installed"}
	    ]
	  }
	}`

	if got := uvInstalledCount(jsonOut); got != 3 {
		t.Errorf("uvInstalledCount = %d, want 3", got)
	}
	if got := uvInstalledCount("not json"); got != 0 {
		t.Errorf("uvInstalledCount(invalid) = %d, want 0", got)
	}
	if got := uvInstalledCount(""); got != 0 {
		t.Errorf("uvInstalledCount(empty) = %d, want 0", got)
	}
}
