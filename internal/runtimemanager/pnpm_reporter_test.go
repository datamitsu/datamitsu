package runtimemanager

import (
	"fmt"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/ui"
)

// TestPNPMReporterCounters feeds a representative pnpm --reporter=ndjson stream
// (captured from pnpm 11.5.1; pnpm 12 emits the same events) and asserts the
// reconstructed resolved/downloaded/added counters and that non-JSON lines do
// not count as events. A nil *ui.Spinner is passed deliberately: SetLabel
// guards a nil receiver, so the parser is exercised without producing output.
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
	if got := rep.errorOutput("fallback"); got != "this is not json" {
		t.Errorf("errorOutput = %q, want the non-JSON line", got)
	}
}

// TestPNPMReporterFallback pins that a stream of events alone yields the
// caller's fallback text.
func TestPNPMReporterFallback(t *testing.T) {
	var sp *ui.Spinner
	rep := newPNPMReporter(sp)
	rep.line([]byte(`{"name":"pnpm:stage","stage":"resolution_started"}`))
	rep.line([]byte(`{"name":"pnpm:stats","added":1}`))

	if got := rep.errorOutput("stdout-fallback"); got != "stdout-fallback" {
		t.Errorf("errorOutput = %q, want fallback", got)
	}
}

// TestPNPMReporterError verifies that an ndjson error event is extracted into
// human-readable error output.
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
		t.Errorf("errorOutput should prefer ndjson errors over the fallback: %q", out)
	}
}

// pnpm12OutdatedLockfile is how pnpm 12 reports a failed frozen install: plain
// text on stderr, interleaved with the ndjson event stream.
var pnpm12OutdatedLockfile = []string{
	`{"name":"pnpm:stage","level":"debug","stage":"resolution_started"}`,
	`Error: ERR_PNPM_OUTDATED_LOCKFILE`,
	``,
	`  × installing dependencies`,
	`  ╰─▶ Cannot install with "frozen-lockfile" because pnpm-lock.yaml is not up`,
	`      to date with package.json.`,
	`  help: Regenerate the lockfile with ` + "`pnpm install --lockfile-only`" + ` so that`,
	`        pnpm-lock.yaml reflects the current package.json.`,
	``,
}

func TestPNPMReporterPlainTextError(t *testing.T) {
	var sp *ui.Spinner
	rep := newPNPMReporter(sp)
	for _, l := range pnpm12OutdatedLockfile {
		rep.line([]byte(l))
	}

	out := rep.errorOutput("fallback")
	if !strings.HasPrefix(out, "Error: ERR_PNPM_OUTDATED_LOCKFILE") {
		t.Errorf("errorOutput should start with the pnpm error code, got %q", out)
	}
	for _, want := range []string{"\n  × installing dependencies", "not up\n      to date", "help: Regenerate the lockfile"} {
		if !strings.Contains(out, want) {
			t.Errorf("errorOutput missing %q (indentation must be kept): %q", want, out)
		}
	}
	if strings.Contains(out, "pnpm:stage") {
		t.Errorf("errorOutput leaked an ndjson event: %q", out)
	}
	if strings.Contains(out, "fallback") {
		t.Errorf("errorOutput used the fallback although pnpm printed an error: %q", out)
	}
}

func TestPNPMReporterNDJSONErrorWinsOverText(t *testing.T) {
	var sp *ui.Spinner
	rep := newPNPMReporter(sp)
	for _, l := range pnpm12OutdatedLockfile {
		rep.line([]byte(l))
	}
	rep.line([]byte(`{"name":"pnpm","level":"error","code":"ERR_PNPM_FETCH_404","err":{"message":"Not Found - 404"}}`))

	out := rep.errorOutput("fallback")
	if !strings.Contains(out, "Not Found - 404") {
		t.Errorf("errorOutput missing the ndjson error: %q", out)
	}
	if strings.Contains(out, "ERR_PNPM_OUTDATED_LOCKFILE") {
		t.Errorf("errorOutput should prefer ndjson error events over plain text: %q", out)
	}
}

// TestPNPMReporterTextBounded pins that a chatty failure cannot grow the kept
// text without bound, and that the tail — where pnpm prints the error — is
// what survives.
func TestPNPMReporterTextBounded(t *testing.T) {
	var sp *ui.Spinner
	rep := newPNPMReporter(sp)
	const extra = 50
	for i := range maxPNPMTextLines + extra {
		rep.line(fmt.Appendf(nil, "line %d", i))
	}

	if len(rep.text) != maxPNPMTextLines {
		t.Fatalf("kept %d text lines, want %d", len(rep.text), maxPNPMTextLines)
	}
	lines := strings.Split(rep.errorOutput(""), "\n")
	if lines[0] != fmt.Sprintf("line %d", extra) {
		t.Errorf("first kept line = %q, want %q", lines[0], fmt.Sprintf("line %d", extra))
	}
	if last := lines[len(lines)-1]; last != fmt.Sprintf("line %d", maxPNPMTextLines+extra-1) {
		t.Errorf("last kept line = %q, want the final line", last)
	}
}
