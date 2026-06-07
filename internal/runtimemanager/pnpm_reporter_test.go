package runtimemanager

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/ui"
)

// TestPNPMReporterCounters feeds a representative pnpm --reporter=ndjson stream
// (captured from pnpm 11.5.1) and asserts the reconstructed resolved/downloaded/
// added counters and that malformed lines are ignored. A nil *ui.Spinner is
// passed deliberately: SetLabel guards a nil receiver, so the parser is exercised
// without producing output.
func TestPNPMReporterCounters(t *testing.T) {
	var sp *ui.Spinner
	rep := newPNPMReporter(sp)

	lines := []string{
		`{"name":"pnpm:stage","stage":"resolution_started"}`,
		`{"name":"pnpm:progress","status":"resolved","packageId":"a@1"}`,
		`{"name":"pnpm:progress","status":"resolved","packageId":"b@1"}`,
		`{"name":"pnpm:progress","status":"resolved","packageId":"c@1"}`,
		`{"name":"pnpm:progress","status":"fetched","packageId":"a@1"}`,
		`{"name":"pnpm:progress","status":"fetched","packageId":"b@1"}`,
		`{"name":"pnpm:progress","status":"imported","packageId":"a@1"}`,
		`{"name":"pnpm:stats","removed":0}`,
		`{"name":"pnpm:stats","added":3}`,
		`this is not json`,
		``,
	}
	for _, l := range lines {
		rep.line([]byte(l))
	}

	if rep.resolved != 3 {
		t.Errorf("resolved = %d, want 3", rep.resolved)
	}
	if rep.downloaded != 2 {
		t.Errorf("downloaded = %d, want 2", rep.downloaded)
	}
	if rep.added != 3 {
		t.Errorf("added = %d, want 3", rep.added)
	}

	// No error events yet: errorOutput falls back to the captured stderr.
	if got := rep.errorOutput("stderr-fallback"); got != "stderr-fallback" {
		t.Errorf("errorOutput without errors = %q, want fallback", got)
	}
}

// TestPNPMReporterError verifies that an ndjson error event (pnpm emits these on
// stdout, not stderr) is extracted into human-readable error output.
func TestPNPMReporterError(t *testing.T) {
	var sp *ui.Spinner
	rep := newPNPMReporter(sp)

	rep.line([]byte(`{"name":"pnpm","level":"error","code":"ERR_PNPM_FETCH_404","hint":"pkg is not in the npm registry","err":{"message":"GET https://registry.npmjs.org/pkg: Not Found - 404"}}`))

	out := rep.errorOutput("stderr-fallback")
	if !strings.Contains(out, "404") {
		t.Errorf("errorOutput missing error message: %q", out)
	}
	if !strings.Contains(out, "not in the npm registry") {
		t.Errorf("errorOutput missing hint: %q", out)
	}
	if strings.Contains(out, "stderr-fallback") {
		t.Errorf("errorOutput should prefer ndjson errors over stderr: %q", out)
	}
}
