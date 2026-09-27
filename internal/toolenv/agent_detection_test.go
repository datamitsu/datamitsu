package toolenv

import (
	"slices"
	"strings"
	"testing"
)

// oxlintAgentVars is what oxlint's agent detection reads, taken from
// apps/oxlint/src/agent_detection.rs of oxc-project/oxc at
// oxlintAgentDetectionCommit, the last commit that touched the file when it was
// taken. Presence of any of them, or a non-empty AI_AGENT, switches oxlint to
// its agent output format. Updating the snapshot is a deliberate change: read
// the file at the new commit and list every name it passes to has_any_env or
// env_string.
const (
	oxlintAgentDetectionCommit = "d193f8eb6908db99c27ce81eb492d0cb5ce126db"
	oxlintAgentVars            = "AI_AGENT CLAUDECODE CLAUDE_CODE REPL_ID GEMINI_CLI CODEX_SANDBOX CODEX_THREAD_ID " +
		"COPILOT_CLI OPENCODE JUNIE_DATA JUNIE_SHIM_PATH CURSOR_AGENT"
	// oxlintAgentHeuristics are read for their content (a .pi/agent segment in
	// PATH, devin in EDITOR, kiro in TERM_PROGRAM), which no stripping can
	// address; they must be listed as kept, with the reason.
	oxlintAgentHeuristics = "PATH EDITOR TERM_PROGRAM"
)

// uncovered returns the names neither stripped by exact and prefix nor listed
// in keep.
func uncovered(names []string, exact, prefix, keep []Entry) []string {
	var out []string
	for _, name := range names {
		if strippedBy(name, exact, prefix) {
			continue
		}
		if !slices.ContainsFunc(keep, func(e Entry) bool { return e.Name == name }) {
			out = append(out, name)
		}
	}
	return out
}

func TestOxlintAgentDetectionIsCovered(t *testing.T) {
	if got := uncovered(strings.Fields(oxlintAgentVars), exactNames, prefixes, kept); len(got) > 0 {
		t.Errorf("oxlint (oxc %s) detects an agent from %v, which is neither stripped nor kept with a reason",
			oxlintAgentDetectionCommit[:8], got)
	}
	for name := range strings.FieldsSeq(oxlintAgentHeuristics) {
		if Stripped(name) || !slices.ContainsFunc(kept, func(e Entry) bool { return e.Name == name }) {
			t.Errorf("%s: oxlint reads its content as an agent marker; it must be kept and say so", name)
		}
	}
}

func TestUncoveredCatchesARemovedName(t *testing.T) {
	names := strings.Fields(oxlintAgentVars)
	withoutCursor := slices.DeleteFunc(slices.Clone(exactNames), func(e Entry) bool { return e.Name == "CURSOR_AGENT" })
	if got := uncovered(names, withoutCursor, prefixes, kept); !slices.Equal(got, []string{"CURSOR_AGENT"}) {
		t.Errorf("uncovered = %v, want the name taken off the list", got)
	}
	withoutJunie := slices.DeleteFunc(slices.Clone(prefixes), func(e Entry) bool { return e.Name == "JUNIE_" })
	if got := uncovered(names, exactNames, withoutJunie, kept); !slices.Equal(got, []string{"JUNIE_DATA", "JUNIE_SHIM_PATH"}) {
		t.Errorf("uncovered = %v, want the names the removed prefix covered", got)
	}
	withoutReplit := slices.DeleteFunc(slices.Clone(kept), func(e Entry) bool { return e.Name == "REPL_ID" })
	if got := uncovered(names, exactNames, prefixes, withoutReplit); !slices.Equal(got, []string{"REPL_ID"}) {
		t.Errorf("uncovered = %v, want the kept name whose reason was removed", got)
	}
}
