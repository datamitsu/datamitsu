package lsp

import (
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// The session policy is ambient — it applies to every save — so it may narrow
// what runs but never widen it. "repo" is not a legal editor policy at all: a
// repository-wide fix on save is never acceptable.
func TestEditorWidenTo(t *testing.T) {
	tests := []struct {
		set  string
		want config.WidenTo
	}{
		{"target", config.WidenToTarget},
		{"unit", config.WidenToUnit},
		{"repo", config.WidenToUnit},
		{"", config.WidenToUnit},
		{"nonsense", config.WidenToUnit},
	}

	for _, tt := range tests {
		t.Run("policy="+tt.set, func(t *testing.T) {
			t.Setenv("DATAMITSU_LSP_FORMAT_WIDEN_TO", tt.set)
			if got := editorWidenTo(); got != tt.want {
				t.Errorf("editorWidenTo() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A unit operation with no globs is planned once per project regardless of what
// was saved, so without a containment check saving one file runs it in every
// module — and these tools fix in place, rewriting files the editor never opened.
func TestTaskCoversPath(t *testing.T) {
	unit := filepath.FromSlash("/repo/svc/a")

	tests := []struct {
		name string
		task tooling.Task
		path string
		want bool
	}{
		{
			"file inside the unit",
			tooling.Task{ProjectPath: unit},
			filepath.Join(unit, "main.go"), true,
		},
		{
			"file in a nested directory",
			tooling.Task{ProjectPath: unit},
			filepath.Join(unit, "internal", "x.go"), true,
		},
		{
			"file in a sibling unit",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/svc/b/main.go"), false,
		},
		{
			// "/repo/svc/ab" must not count as inside "/repo/svc/a".
			"sibling with a shared name prefix",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/svc/ab/main.go"), false,
		},
		{
			"file above the unit",
			tooling.Task{ProjectPath: unit},
			filepath.FromSlash("/repo/main.go"), false,
		},
		{
			// No project path means the task is not unit-bound.
			"task with no unit covers everything",
			tooling.Task{},
			filepath.FromSlash("/repo/anywhere.go"), true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskCoversPath(tt.task, tt.path); got != tt.want {
				t.Errorf("taskCoversPath(%q, %q) = %v, want %v",
					tt.task.ProjectPath, tt.path, got, tt.want)
			}
		})
	}
}
