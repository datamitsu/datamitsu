package report

import (
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestShownOf(t *testing.T) {
	code := 1
	d := func(reported bool) diagnostic.Diagnostic { return diagnostic.Diagnostic{Reported: reported} }
	tests := []struct {
		name string
		proc tooling.ProcessResult
		want []bool
	}{
		{
			name: "a failed process shows what reaches the threshold",
			proc: tooling.ProcessResult{
				State: tooling.ProcessRan, ExitCode: &code, FailOn: "error", GateActive: true,
				Diagnostics: []diagnostic.Diagnostic{d(true), d(false)},
			},
			want: []bool{true, false},
		},
		{
			name: "a failure none of whose findings reaches it shows them all",
			proc: tooling.ProcessResult{
				State: tooling.ProcessRan, ExitCode: &code, FailOn: "error", GateActive: true,
				Diagnostics: []diagnostic.Diagnostic{d(false), d(false)},
			},
			want: []bool{true, true},
		},
		{
			name: "a passed process shows nothing",
			proc: tooling.ProcessResult{
				State: tooling.ProcessRan, Success: true, FailOn: "error", GateActive: true,
				Diagnostics: []diagnostic.Diagnostic{d(true)},
			},
			want: []bool{false},
		},
		{
			name: "an unenforced threshold shows what it would have caught",
			proc: tooling.ProcessResult{
				State: tooling.ProcessRan, Success: true, FailOn: "warning",
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
	inv := Invocation{Findings: []Finding{
		{Kind: kindIssue, Message: "shown", Shown: true},
		{Kind: kindIssue, Message: "hidden"},
		{Kind: kindSynthetic, Message: "t exited 1 without parsable findings"},
	}}
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
			Tool: "t", Source: "t", Code: "c", Severity: "error", Reported: true, Kind: kindIssue, Shown: shown,
			Message: "m", Location: Location{Path: "a.go", Row: 1},
		}
	}
	warning := Finding{
		Tool: "t", Source: "t", Code: "w", Severity: "warning", Kind: kindIssue, Message: "below",
		Location: Location{Path: "a.go", Row: 2},
	}
	tr := &ToolRun{Name: "t", Invocations: []Invocation{
		{ID: "t::1#1", State: "ran", ExitCode: &code, Findings: []Finding{dup(false)}},
		{ID: "t::1#2", State: "ran", ExitCode: &code, Findings: []Finding{dup(true), warning}},
	}}
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
