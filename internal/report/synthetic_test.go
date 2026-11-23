package report

import (
	"strings"
	"testing"
	zerovalue_time "time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
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
			category: "",
			name:     "exited non-zero without findings",
			proc:     tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, ExitCode: new(2), OutputTail: []byte("boom")},
			want:     "tsc exited 2 without parsable findings",
		},
		{
			name: "a security tool", category: "security",
			proc: tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, ExitCode: new(1), OutputTail: []byte("the-found-secret")},
			want: "tsc failed (exit 1); output withheld for a security tool",
		},
		{
			category: "",
			want:     "",
			name:     "exited non-zero with a finding",
			proc:     tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, ExitCode: new(1), Diagnostics: []diagnostic.Diagnostic{{File: "", Row: 0, Col: 0, EndRow: 0, EndCol: 0, Severity: 0, Source: "", Code: "", URL: "", Reported: false, Gates: false, Baselined: false, Anchor: nil, Message: "m"}}},
		},
		{
			category: "",
			want:     "",
			name:     "failed on its threshold",
			proc:     tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, State: tooling.ProcessRan, ExitCode: new(0), ThresholdFailed: true},
		},
		{category: "", want: "", name: "cancelled", proc: tooling.ProcessResult{ID: "", Files: nil, ExitCode: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessCancelled}},
		{category: "", want: "", name: "not started", proc: tooling.ProcessResult{ID: "", Files: nil, ExitCode: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessNotStarted}},
		{category: "", want: "", name: "could not start", proc: tooling.ProcessResult{ID: "", Files: nil, ExitCode: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessSetupFailed}},
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
	failed := tooling.ProcessResult{ID: "", Files: nil, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, Success: false, ExitCode: new(1)}
	tests := []struct {
		name     string
		proc     tooling.ProcessResult
		tail     string
		category string
		want     string
	}{
		{category: "", name: "a failure", proc: failed, tail: "\x1b[31merror\x1b[0m: x\n", want: "error: x\n"},
		{category: "", name: "cut inside a character", proc: failed, tail: "\xa9 é", want: " é"},
		{category: "", name: "invalid bytes", proc: failed, tail: "a\xffb", want: "a�b"},
		{want: "", name: "a security tool", proc: failed, tail: "secret", category: "security"},
		{category: "", want: "", name: "a success", proc: tooling.ProcessResult{ID: "", Files: nil, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, Success: true, ExitCode: new(0)}, tail: "ok"},
		{category: "", name: "could not start", proc: tooling.ProcessResult{ID: "", Files: nil, ExitCode: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessSetupFailed}, tail: "no stdin", want: "no stdin"},
		{category: "", want: "", name: "cancelled", proc: tooling.ProcessResult{ID: "", Files: nil, ExitCode: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessCancelled}, tail: "partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.proc.OutputTail = []byte(tt.tail)
			if got := outputTail(tt.proc, tt.category == "security", nil); got != tt.want {
				t.Errorf("outputTail() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A tail cut from a longer output through a secret keeps no part of it: masking
// cannot recognize a fragment, so the fragment is not kept.
func TestOutputTailCutThroughASecret(t *testing.T) {
	const secret = "abcdefgh12"
	output := strings.Repeat("y", 100) + secret + strings.Repeat("x", tooling.OutputTailBytes-6)
	cut := []byte(output[len(output)-tooling.OutputTailBytes:])
	if !strings.HasPrefix(string(cut), secret[4:]) {
		t.Fatalf("the fixture does not cut through the secret: %q", cut[:10])
	}
	proc := tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, ExitCode: new(1), OutputTail: cut}
	secrets := []string{secret}
	tail := outputTail(proc, false, secrets)
	if strings.Contains(tail, secret[4:]) || len(tail) != tooling.OutputTailBytes-len(secret[4:]) {
		t.Errorf("tail keeps %d bytes starting %q; want the fragment gone", len(tail), tail[:min(10, len(tail))])
	}

	// A whole secret just after the cut is masked whole, not cut again.
	whole := []byte(strings.Repeat("x", 5) + secret + strings.Repeat("y", tooling.OutputTailBytes-5-len(secret)))
	tail = outputTail(tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, ExitCode: new(1), OutputTail: whole}, false, secrets)
	if !strings.HasPrefix(tail, "xxxxx"+Masked+"y") {
		t.Errorf("tail starts %q; want the secret masked whole", tail[:min(20, len(tail))])
	}

	if untouched := outputTail(tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, ExitCode: new(1), OutputTail: []byte("short " + secret)}, false, secrets); untouched != "short "+secret {
		t.Errorf("a tail that was not cut lost bytes: %q", untouched)
	}
}

// A tool whose parser the run could not describe — its module never loaded,
// or does not list the key — may be a security tool: its output is withheld,
// as a security tool's is.
func TestOutputTailOfAToolOfUnknownCategory(t *testing.T) {
	acc := NewAccumulator(Options{
		Apps:    nil,
		FailOn:  "",
		Secrets: nil,
		Root:    root,
		Tools: config.MapOfTools{
			"scanner":  {Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: &config.OutputParser{Module: "missing", Parser: "scanner"}},
			"unlisted": {Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: &config.OutputParser{Module: "core", Parser: "no-such-parser"}},
			"linter":   {Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: &config.OutputParser{Module: "core", Parser: "linter"}},
			"plain":    {Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
		},
		Parsers: func(module, parser string) (parsermanager.ParserFacts, bool) {
			if module != "core" {
				return parsermanager.ParserFacts{Version: "", Schema: 0, Contract: false, Tool: parsermanager.ToolCapability{Name: "", Description: "", URL: "", Operations: nil, Severities: nil, ColumnUnit: "", Category: "", Kind: ""}}, false
			}
			if parser == "linter" {
				return parsermanager.ParserFacts{Version: "", Schema: 0, Contract: false, Tool: parsermanager.ToolCapability{Description: "", URL: "", Operations: nil, Severities: nil, ColumnUnit: "", Category: "", Kind: "", Name: parser}}, true
			}
			return parsermanager.ParserFacts{Version: "", Schema: 0, Contract: false, Tool: parsermanager.ToolCapability{Name: "", Description: "", URL: "", Operations: nil, Severities: nil, ColumnUnit: "", Category: "", Kind: ""}}, true
		},
	})
	for name, want := range map[string]string{"scanner": "", "unlisted": "", "linter": "boom\n", "plain": "boom\n"} {
		tr := acc.newToolRun("lint", name)
		task := perFileTask(name+"::1", "a")
		task.ToolName, task.Tool = name, acc.opts.Tools[name]
		result := tooling.ExecutionResult{
			ToolName:         "",
			Success:          false,
			Output:           "",
			Error:            nil,
			Duration:         0,
			Command:          "",
			ExitCode:         0,
			WorkingDir:       "",
			RelativeDir:      "",
			Scope:            "",
			Batch:            false,
			Cancelled:        false,
			FailureReason:    0,
			StartedAt:        zerovalue_time.Time{},
			EndedAt:          zerovalue_time.Time{},
			FilesNotRun:      0,
			UnparsedFailures: nil,
			CapturedStdout:   "",
			Diagnostics:      nil,
			ParseFailed:      false,
			FailOn:           "",
			Files:            nil,
			FileResults:      nil,
			Cached:           false,
			WholeUnit:        false,
			UnitDir:          "", TaskID: task.ID, Processes: []tooling.ProcessResult{{
				Success:         false,
				Extraction:      "",
				ParseError:      "",
				Provenance:      "",
				ParserModule:    "",
				Diagnostics:     nil,
				DurationMs:      0,
				FailOn:          "",
				GateActive:      false,
				ThresholdFailed: false,
				ID:              task.ID + "#1", Files: task.Files, State: tooling.ProcessRan, ExitCode: new(1), OutputTail: []byte("boom\n"),
			}},
		}
		if got := acc.invocations(task, &result, nil, tr)[0].OutputTail; got != want {
			t.Errorf("%s: output tail = %q, want %q", name, got, want)
		}
	}
}

// Two invocations that failed alike keep a synthetic finding each, with
// distinct fingerprints.
func TestSyntheticFindingsAreNotMerged(t *testing.T) {
	f, _ := syntheticFinding("tsc", tooling.ProcessResult{ID: "", Files: nil, Success: false, Extraction: "", ParseError: "", Provenance: "", ParserModule: "", OutputTail: nil, Diagnostics: nil, DurationMs: 0, FailOn: "", GateActive: false, ThresholdFailed: false, State: tooling.ProcessRan, ExitCode: new(2)}, "")
	tr := &ToolRun{
		RedactedReasons: nil,
		Name:            "", App: AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Incomplete: nil, mayHoldSecrets: false, Invocations: []Invocation{{
			RedactedState:      0,
			RedactedExtraction: 0,
			TaskID:             "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", ID: "tsc:a:1#1", Findings: []Finding{f},
		}, {
			RedactedState:      0,
			RedactedExtraction: 0,
			TaskID:             "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", ID: "tsc:b:2#1", Findings: []Finding{f},
		}},
	}
	settleFindings(tr)
	a, b := tr.Invocations[0].Findings, tr.Invocations[1].Findings
	if len(a) != 1 || len(b) != 1 || a[0].Fingerprint == b[0].Fingerprint {
		t.Errorf("synthetic findings = %+v, %+v; want one each with distinct fingerprints", a, b)
	}
}
