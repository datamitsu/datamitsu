package report

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestSyntheticFinding(t *testing.T) {
	tests := []struct {
		name     string
		proc     tooling.ProcessResult
		category string
		want     string
	}{
		{
			name: "exited non-zero without findings",
			proc: tooling.ProcessResult{State: tooling.ProcessRan, ExitCode: new(2), OutputTail: []byte("boom")},
			want: "tsc exited 2 without parsable findings",
		},
		{
			name: "a security tool", category: "security",
			proc: tooling.ProcessResult{State: tooling.ProcessRan, ExitCode: new(1), OutputTail: []byte("the-found-secret")},
			want: "tsc failed (exit 1); output withheld for a security tool",
		},
		{
			name: "exited non-zero with a finding",
			proc: tooling.ProcessResult{State: tooling.ProcessRan, ExitCode: new(1), Diagnostics: []diagnostic.Diagnostic{{Message: "m"}}},
		},
		{
			name: "failed on its threshold",
			proc: tooling.ProcessResult{State: tooling.ProcessRan, ExitCode: new(0), ThresholdFailed: true},
		},
		{name: "cancelled", proc: tooling.ProcessResult{State: tooling.ProcessCancelled}},
		{name: "not started", proc: tooling.ProcessResult{State: tooling.ProcessNotStarted}},
		{name: "could not start", proc: tooling.ProcessResult{State: tooling.ProcessSetupFailed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := syntheticFinding("tsc", tt.proc, tt.category)
			if ok != (tt.want != "") {
				t.Fatalf("synthetic finding = %+v, %v; want one: %v", f, ok, tt.want != "")
			}
			if !ok {
				return
			}
			if f.Message != tt.want || f.Kind != "synthetic" || f.Severity != "error" || f.Reported || !f.Gates ||
				f.Provenance != "synthetic" || f.FingerprintBasis != "none" || f.Location.Path != "" || f.Location.Row != 0 {
				t.Errorf("synthetic finding = %+v", f)
			}
			if strings.Contains(f.Message, string(tt.proc.OutputTail)) {
				t.Error("the synthetic message carries captured output")
			}
		})
	}
}

func TestOutputTail(t *testing.T) {
	failed := tooling.ProcessResult{State: tooling.ProcessRan, Success: false, ExitCode: new(1)}
	tests := []struct {
		name     string
		proc     tooling.ProcessResult
		tail     string
		category string
		want     string
	}{
		{name: "a failure", proc: failed, tail: "\x1b[31merror\x1b[0m: x\n", want: "error: x\n"},
		{name: "cut inside a character", proc: failed, tail: "\xa9 é", want: " é"},
		{name: "invalid bytes", proc: failed, tail: "a\xffb", want: "a�b"},
		{name: "a security tool", proc: failed, tail: "secret", category: "security"},
		{name: "a success", proc: tooling.ProcessResult{State: tooling.ProcessRan, Success: true, ExitCode: new(0)}, tail: "ok"},
		{name: "could not start", proc: tooling.ProcessResult{State: tooling.ProcessSetupFailed}, tail: "no stdin", want: "no stdin"},
		{name: "cancelled", proc: tooling.ProcessResult{State: tooling.ProcessCancelled}, tail: "partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.proc.OutputTail = []byte(tt.tail)
			if got := outputTail(tt.proc, tt.category); got != tt.want {
				t.Errorf("outputTail() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Two invocations that failed alike keep a synthetic finding each, with
// distinct fingerprints.
func TestSyntheticFindingsAreNotMerged(t *testing.T) {
	f, _ := syntheticFinding("tsc", tooling.ProcessResult{State: tooling.ProcessRan, ExitCode: new(2)}, "")
	tr := &ToolRun{Invocations: []Invocation{{ID: "tsc:a:1#1", Findings: []Finding{f}}, {ID: "tsc:b:2#1", Findings: []Finding{f}}}}
	settleFindings(tr)
	a, b := tr.Invocations[0].Findings, tr.Invocations[1].Findings
	if len(a) != 1 || len(b) != 1 || a[0].Fingerprint == b[0].Fingerprint {
		t.Errorf("synthetic findings = %+v, %+v; want one each with distinct fingerprints", a, b)
	}
}
