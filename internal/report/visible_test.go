package report

import (
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestShownOf(t *testing.T) {
	code := 1
	d := func(reported bool) diagnostic.Diagnostic {
		return diagnostic.Diagnostic{File: "", Row: 0, Col: 0, EndRow: 0, EndCol: 0, Severity: 0, Message: "", Source: "", Code: "", URL: "", Gates: false, Baselined: false, Anchor: nil, Reported: reported}
	}
	tests := []struct {
		name string
		proc tooling.ProcessResult
		want []bool
	}{
		{
			name: "a failed process shows what reaches the threshold",
			proc: tooling.ProcessResult{
				ID:              "",
				Files:           nil,
				Success:         false,
				Extraction:      "",
				ParseError:      "",
				Provenance:      "",
				ParserModule:    "",
				OutputTail:      nil,
				DurationMs:      0,
				ThresholdFailed: false,
				State:           tooling.ProcessRan, ExitCode: &code, FailOn: "error", GateActive: true,
				Diagnostics: []diagnostic.Diagnostic{d(true), d(false)},
			},
			want: []bool{true, false},
		},
		{
			name: "a failure none of whose findings reaches it shows them all",
			proc: tooling.ProcessResult{
				ID:              "",
				Files:           nil,
				Success:         false,
				Extraction:      "",
				ParseError:      "",
				Provenance:      "",
				ParserModule:    "",
				OutputTail:      nil,
				DurationMs:      0,
				ThresholdFailed: false,
				State:           tooling.ProcessRan, ExitCode: &code, FailOn: "error", GateActive: true,
				Diagnostics: []diagnostic.Diagnostic{d(false), d(false)},
			},
			want: []bool{true, true},
		},
		{
			name: "a passed process shows nothing",
			proc: tooling.ProcessResult{
				ID:              "",
				Files:           nil,
				ExitCode:        nil,
				Extraction:      "",
				ParseError:      "",
				Provenance:      "",
				ParserModule:    "",
				OutputTail:      nil,
				DurationMs:      0,
				ThresholdFailed: false,
				State:           tooling.ProcessRan, Success: true, FailOn: "error", GateActive: true,
				Diagnostics: []diagnostic.Diagnostic{d(true)},
			},
			want: []bool{false},
		},
		{
			name: "an unenforced threshold shows what it would have caught",
			proc: tooling.ProcessResult{
				ID:              "",
				Files:           nil,
				ExitCode:        nil,
				Extraction:      "",
				ParseError:      "",
				Provenance:      "",
				ParserModule:    "",
				OutputTail:      nil,
				DurationMs:      0,
				GateActive:      false,
				ThresholdFailed: false,
				State:           tooling.ProcessRan, Success: true, FailOn: "warning",
				Diagnostics: []diagnostic.Diagnostic{d(true), d(false)},
			},
			want: []bool{true, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShownOf(tt.proc); !slices.Equal(got, tt.want) {
				t.Errorf("ShownOf() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisible(t *testing.T) {
	inv := Invocation{
		RedactedState:      0,
		RedactedExtraction: 0,

		ID:          "",
		TaskID:      "",
		Dir:         "",
		Scope:       "",
		Granularity: "",
		Arity:       "",
		Coverage:    "",
		WholeUnit:   false,
		State:       "",
		ExitCode:    nil,
		Success:     false,
		FailureKind: "",
		Duration:    0,
		Extraction:  "",
		Provenance:  "",
		Files:       nil,
		Step:        0,
		Changes:     nil,
		OutputTail:  "", Findings: []Finding{
			{Fingerprint: "", FingerprintBasis: "", Tool: "", Source: "", Code: "", RuleURL: "", Severity: "", Reported: false, Gates: false, Baselined: false, Location: Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: ""}, Provenance: "", lineHash: "", Kind: kindIssue, Message: "shown", Shown: true},
			{Fingerprint: "", FingerprintBasis: "", Tool: "", Source: "", Code: "", RuleURL: "", Severity: "", Reported: false, Gates: false, Shown: false, Baselined: false, Location: Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: ""}, Provenance: "", lineHash: "", Kind: kindIssue, Message: "hidden"},
			{Fingerprint: "", FingerprintBasis: "", Tool: "", Source: "", Code: "", RuleURL: "", Severity: "", Reported: false, Gates: false, Shown: false, Baselined: false, Location: Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: ""}, Provenance: "", lineHash: "", Kind: kindSynthetic, Message: "t exited 1 without parsable findings"},
		},
	}
	shown, hidden := Visible(inv)
	if len(shown) != 1 || shown[0].Message != "shown" || len(hidden) != 1 || hidden[0].Message != "hidden" {
		t.Errorf("Visible() = %v, %v; want the shown finding and the hidden one, the synthetic in neither", shown, hidden)
	}
	if got, ok := Synthetic(inv); !ok || got.Kind != kindSynthetic {
		t.Errorf("Synthetic() = %v, %v", got, ok)
	}
}

// A finding two processes of a tool both reported is listed once, and is
// shown when either process showed it: dropping the duplicate never changes
// what the other findings of its process show.
func TestSettleKeepsWhatWasShown(t *testing.T) {
	code := 1
	dup := func(shown bool) Finding {
		return Finding{
			Fingerprint:      "",
			FingerprintBasis: "",
			RuleURL:          "",
			Gates:            false,
			Baselined:        false,
			Provenance:       "",
			lineHash:         "",
			Tool:             "t", Source: "t", Code: "c", Severity: "error", Reported: true, Kind: kindIssue, Shown: shown,
			Message: "m", Location: Location{EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: "", Path: "a.go", Row: 1},
		}
	}
	warning := Finding{
		Fingerprint:      "",
		FingerprintBasis: "",
		RuleURL:          "",
		Reported:         false,
		Gates:            false,
		Shown:            false,
		Baselined:        false,
		Provenance:       "",
		lineHash:         "",
		Tool:             "t", Source: "t", Code: "w", Severity: "warning", Kind: kindIssue, Message: "below",
		Location: Location{EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: "", Path: "a.go", Row: 2},
	}
	tr := &ToolRun{
		RedactedReasons: nil,

		App:            AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
		Parser:         nil,
		FailOn:         "",
		GateActive:     false,
		Category:       "",
		Complete:       false,
		Incomplete:     nil,
		mayHoldSecrets: false, Name: "t", Invocations: []Invocation{
			{
				RedactedState:      0,
				RedactedExtraction: 0,
				TaskID:             "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", ID: "t::1#1", State: "ran", ExitCode: &code, Findings: []Finding{dup(false)},
			},
			{
				RedactedState:      0,
				RedactedExtraction: 0,
				TaskID:             "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Step: 0, Changes: nil, OutputTail: "", ID: "t::1#2", State: "ran", ExitCode: &code, Findings: []Finding{dup(true), warning},
			},
		},
	}
	settleFindings(tr)
	var listed, shown []string
	for _, inv := range tr.Invocations {
		for _, f := range inv.Findings {
			listed = append(listed, f.Code)
			if f.Shown {
				shown = append(shown, f.Code)
			}
		}
	}
	if !slices.Equal(listed, []string{"c", "w"}) || !slices.Equal(shown, []string{"c"}) {
		t.Errorf("listed %v, shown %v; want the error once and shown, the warning hidden", listed, shown)
	}
}
