package report

import (
	"slices"
	"testing"
)

func TestVisible(t *testing.T) {
	code := 1
	f := func(msg string, reported bool) Finding {
		return Finding{Kind: kindIssue, Message: msg, Reported: reported}
	}
	synthetic := Finding{Kind: kindSynthetic, Message: "t exited 1 without parsable findings"}
	tests := []struct {
		name          string
		tool          ToolRun
		inv           Invocation
		shown, hidden []string
	}{
		{
			name:  "a failed invocation shows what reaches the threshold",
			tool:  ToolRun{FailOn: "error", GateActive: true},
			inv:   Invocation{State: "ran", ExitCode: &code, Findings: []Finding{f("e", true), f("w", false)}},
			shown: []string{"e"}, hidden: []string{"w"},
		},
		{
			name:  "a failure none of whose findings reaches it shows them all",
			tool:  ToolRun{FailOn: "error", GateActive: true},
			inv:   Invocation{State: "ran", ExitCode: &code, Findings: []Finding{f("w1", false), f("w2", false)}},
			shown: []string{"w1", "w2"},
		},
		{
			name:   "a passed invocation shows nothing",
			tool:   ToolRun{FailOn: "error", GateActive: true},
			inv:    Invocation{State: "ran", Success: true, Findings: []Finding{f("w", false)}},
			hidden: []string{"w"},
		},
		{
			name:  "an unenforced threshold shows what it would have caught",
			tool:  ToolRun{FailOn: "warning"},
			inv:   Invocation{State: "ran", Success: true, Findings: []Finding{f("w", true), f("i", false)}},
			shown: []string{"w"}, hidden: []string{"i"},
		},
		{
			name: "a synthetic finding is in neither",
			tool: ToolRun{FailOn: "error", GateActive: true},
			inv:  Invocation{State: "ran", ExitCode: &code, Findings: []Finding{synthetic}},
		},
	}
	messages := func(fs []Finding) []string {
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, f.Message)
		}
		return out
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shown, hidden := Visible(tt.tool, tt.inv)
			if !slices.Equal(messages(shown), tt.shown) || !slices.Equal(messages(hidden), tt.hidden) {
				t.Errorf("shown %v, hidden %v; want %v, %v", messages(shown), messages(hidden), tt.shown, tt.hidden)
			}
		})
	}
	if got, ok := Synthetic(Invocation{Findings: []Finding{f("e", true), synthetic}}); !ok || got.Message != synthetic.Message {
		t.Errorf("Synthetic() = %v, %v", got, ok)
	}
}
