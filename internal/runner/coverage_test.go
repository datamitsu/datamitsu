package runner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// Rank() reads an unvalidated string through a map, so an unrecognised value
// ranks 0 — the same as the strictest level. Unchecked, --widen-to=Repo asks for
// the widest policy and silently gets the narrowest.
func TestOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{"empty", Options{}, ""},
		{"widen target", Options{WidenTo: "target"}, ""},
		{"widen unit", Options{WidenTo: "unit"}, ""},
		{"widen repo", Options{WidenTo: "repo"}, ""},
		{"widen wrong case", Options{WidenTo: "Repo"}, "invalid --widen-to"},
		{"widen garbage", Options{WidenTo: "everything"}, "invalid --widen-to"},
		{"require unit", Options{RequireCoverage: "unit"}, ""},
		{"require repo", Options{RequireCoverage: "repo"}, ""},
		// "target" asserts only what was named, which is always true.
		{"require target", Options{RequireCoverage: "target"}, "always true"},
		{"require garbage", Options{RequireCoverage: "everything"}, "invalid --require-coverage"},
		{"require wrong case", Options{RequireCoverage: "Unit"}, "invalid --require-coverage"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() = nil, want an error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validate() = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// --require-coverage must mean the same thing with and without --explain, or it
// is useless as a CI gate: the cheap plan-only check would pass exactly where
// the real run fails. Only the reasons differ from --fail-on-skip, which stays
// inert in explain mode.
func TestExplainAndExecutingPathsAgreeOnCoverage(t *testing.T) {
	skips := []tooling.SkippedTool{
		{ToolName: "syncpack", Reason: tooling.SkipReasonNotNarrowable},
		{ToolName: "typstyle", Reason: tooling.SkipReasonUnsupportedPlatform},
		{ToolName: "bearer", Reason: tooling.SkipReasonConfig},
	}

	explain := &sharedContext{platformSkipped: map[string]struct{}{}}
	explain.recordNarrowingSkips(skips)

	executing := &sharedContext{platformSkipped: map[string]struct{}{}}
	executing.recordSkips(skips)

	if len(explain.narrowed) != len(executing.narrowed) {
		t.Fatalf("explain recorded %v, executing recorded %v", explain.narrowed, executing.narrowed)
	}
	for name := range executing.narrowed {
		if _, ok := explain.narrowed[name]; !ok {
			t.Errorf("%q counts against coverage when running but not when explaining", name)
		}
	}
	if _, ok := explain.narrowed["bearer"]; ok {
		t.Error("a config skip counted against coverage; opting a tool out is an answer, not a gap")
	}
	// --fail-on-skip is about a host lacking a binary, which only matters if
	// something was going to run.
	if len(explain.platformSkipped) != 0 {
		t.Errorf("explain armed --fail-on-skip with %v", explain.platformSkipped)
	}
}

func TestRecordCoverage(t *testing.T) {
	sc := &sharedContext{narrowed: map[string]struct{}{}}
	sc.recordCoverage(&tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{
		{ToolName: "tsc", Coverage: tooling.CoveragePartial},
		{ToolName: "eslint", Coverage: tooling.CoverageComplete},
	}}}})

	if _, ok := sc.narrowed["tsc"]; !ok {
		t.Error("a partial task must count against coverage")
	}
	if _, ok := sc.narrowed["eslint"]; ok {
		t.Error("a complete task must not count against coverage")
	}
}

// A green result on a narrowed run must not read as a green repository.
func TestTargetLine(t *testing.T) {
	root := filepath.FromSlash("/repo")
	paths := func(names ...string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = filepath.Join(root, filepath.FromSlash(n))
		}
		return out
	}

	tests := []struct {
		name string
		sel  tooling.Selection
		want string // "" means no line at all
	}{
		{"whole repository is the common case", tooling.Selection{Mode: tooling.SelectionAll}, ""},
		{"nothing staged", tooling.Selection{Mode: tooling.SelectionEmpty}, "nothing staged"},
		{
			"one file is singular",
			tooling.Selection{Mode: tooling.SelectionPaths, Paths: paths("a.ts")},
			"1 file · a.ts",
		},
		{
			"several files are plural",
			tooling.Selection{Mode: tooling.SelectionPaths, Paths: paths("a.ts", "b.ts")},
			"2 files · a.ts b.ts",
		},
		{
			"a long list is truncated",
			tooling.Selection{Mode: tooling.SelectionPaths, Paths: paths("a.ts", "b.ts", "c.ts", "d.ts", "e.ts")},
			"+2 more",
		},
		{
			"a subtree names the directory",
			tooling.Selection{Mode: tooling.SelectionSubtree, Dir: filepath.Join(root, "pkg")},
			"target: pkg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &sharedContext{rootPath: root, selection: tt.sel}
			got := sc.targetLine()

			if tt.want == "" {
				if got != "" {
					t.Fatalf("targetLine() = %q, want no annotation", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("targetLine() = %q, want it to contain %q", got, tt.want)
			}
			if !strings.Contains(got, "narrowed run") {
				t.Errorf("targetLine() = %q, want it to say the run was narrowed", got)
			}
		})
	}
}

// Paths are shown relative to the git root so the line stays readable and does
// not change with where the repository lives.
func TestRelativeToRoot(t *testing.T) {
	root := filepath.FromSlash("/repo")
	got := relativeToRoot([]string{filepath.Join(root, "pkg", "a.ts")}, root)

	if len(got) != 1 || got[0] != filepath.FromSlash("pkg/a.ts") {
		t.Errorf("relativeToRoot = %v, want [pkg/a.ts]", got)
	}
}

func TestValidWidenTo(t *testing.T) {
	for _, w := range []config.WidenTo{config.WidenToTarget, config.WidenToUnit, config.WidenToRepo} {
		if !config.ValidWidenTo(w) {
			t.Errorf("ValidWidenTo(%q) = false, want true", w)
		}
	}
	for _, w := range []config.WidenTo{"", "Repo", "everything"} {
		if config.ValidWidenTo(w) {
			t.Errorf("ValidWidenTo(%q) = true, want false", w)
		}
	}
}
