package tooling

import (
	"strings"
	"testing"
)

// The mode is logged and rendered in --require-coverage failures, so an
// unlabelled mode would surface as an empty string in user-facing output.
func TestSelectionString(t *testing.T) {
	tests := []struct {
		sel  Selection
		want string
	}{
		{Selection{Dir: "", Paths: nil, Mode: SelectionAll}, "all"},
		{Selection{Paths: nil, Mode: SelectionSubtree, Dir: "/repo/pkg"}, "subtree"},
		{Selection{Dir: "", Mode: SelectionPaths, Paths: []string{"/repo/a.ts"}}, "paths"},
		{Selection{Dir: "", Paths: nil, Mode: SelectionEmpty}, "empty"},
		{Selection{Dir: "", Paths: nil, Mode: SelectionMode(99)}, "all"},
	}

	for _, tt := range tests {
		if got := tt.sel.String(); got != tt.want {
			t.Errorf("Selection{Mode: %d}.String() = %q, want %q", tt.sel.Mode, got, tt.want)
		}
	}
}

// The string is the stable key in --explain=json; a wrong one silently changes
// a machine-readable contract.
func TestSkipReasonStrings(t *testing.T) {
	tests := []struct {
		reason   SkipReason
		key      string
		textPart string
	}{
		{SkipReasonConfig, "config", "disabled in config"},
		{SkipReasonUnsupportedPlatform, "unsupported-platform", "no binary"},
		{SkipReasonNotNarrowable, "not-narrowable", "cannot narrow"},
	}

	for _, tt := range tests {
		if got := tt.reason.String(); got != tt.key {
			t.Errorf("SkipReason(%d).String() = %q, want %q", tt.reason, got, tt.key)
		}
		got := SkippedTool{ToolName: "", Operation: "", Detail: "", Reason: tt.reason}.ReasonText()
		if !strings.Contains(got, tt.textPart) {
			t.Errorf("ReasonText() = %q, want it to mention %q", got, tt.textPart)
		}
	}

	t.Run("detail is preferred when present", func(t *testing.T) {
		got := SkippedTool{ToolName: "", Operation: "", Reason: SkipReasonUnsupportedPlatform, Detail: "linux/riscv64"}.ReasonText()
		if !strings.Contains(got, "linux/riscv64") {
			t.Errorf("ReasonText() = %q, want it to name the platform", got)
		}
	})

	t.Run("config skip shows its configured reason", func(t *testing.T) {
		got := SkippedTool{ToolName: "", Operation: "", Reason: SkipReasonConfig, Detail: "runs in CI only"}.ReasonText()
		if !strings.Contains(got, "runs in CI only") {
			t.Errorf("ReasonText() = %q, want the configured reason", got)
		}
	})
}
