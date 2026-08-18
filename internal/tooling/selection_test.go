package tooling

import (
	"context"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestNewSelection(t *testing.T) {
	tests := []struct {
		name       string
		root, cwd  string
		paths      []string
		fileScoped bool
		want       SelectionMode
	}{
		{"no paths at the root", "/repo", "/repo", nil, false, SelectionAll},
		{"no paths in a subdirectory", "/repo", "/repo/pkg", nil, false, SelectionSubtree},
		{"explicit paths", "/repo", "/repo", []string{"/repo/a.go"}, false, SelectionPaths},
		{"explicit paths from a subdirectory", "/repo", "/repo/pkg", []string{"/repo/pkg/a.go"}, false, SelectionPaths},
		{"staged files", "/repo", "/repo", []string{"/repo/a.go"}, true, SelectionPaths},
		{"nothing staged", "/repo", "/repo", nil, true, SelectionEmpty},
		{"nothing staged in a subdirectory", "/repo", "/repo/pkg", nil, true, SelectionEmpty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewSelection(tt.root, tt.cwd, tt.paths, tt.fileScoped)
			if got.Mode != tt.want {
				t.Errorf("Mode = %v, want %v", got, Selection{Mode: tt.want})
			}
		})
	}
}

func TestSelectionFiles(t *testing.T) {
	paths := []string{"/repo/a.go"}
	if got := (Selection{Mode: SelectionPaths, Paths: paths}).Files(); len(got) != 1 {
		t.Errorf("Files() = %v, want the named paths", got)
	}
	// Every other mode means "work it out from the repository walk", which is not
	// the same as "these zero files".
	for _, m := range []SelectionMode{SelectionAll, SelectionSubtree, SelectionEmpty} {
		if got := (Selection{Mode: m, Paths: paths}).Files(); got != nil {
			t.Errorf("mode %v: Files() = %v, want nil", m, got)
		}
	}
}

// An empty staged set used to be indistinguishable from "no files given", so
// --file-scoped with nothing staged swept every glob and ran the whole
// repository — for a fix operation, a rewrite of files the commit never touched.
func TestCollectTasksEmptySelectionPlansNothing(t *testing.T) {
	planner := &Planner{
		rootPath: "/repo",
		cwdPath:  "/repo",
		tools: config.MapOfTools{
			"globbed": {Operations: map[config.OperationType]config.ToolOperation{
				config.OpFix: {App: "fmt", Args: []string{"{files}"}, Scope: config.ToolScopeRepository, Globs: []string{"**/*.go"}},
			}},
			"whole-project": {Operations: map[config.OperationType]config.ToolOperation{
				config.OpFix: {App: "tidy", Args: []string{"run"}, Scope: config.ToolScopeRepository},
			}},
		},
		cachedFiles:      []string{"/repo/a.go", "/repo/b.go"},
		cacheInitialized: true,
	}

	tasks, skipped := planner.collectTasks(context.Background(), config.OpFix, Selection{Mode: SelectionEmpty})
	if len(tasks) != 0 {
		t.Errorf("empty selection planned %d tasks, want 0: %+v", len(tasks), tasks)
	}
	if len(skipped) != 0 {
		t.Errorf("empty selection reported %d skips, want 0", len(skipped))
	}

	// The same planner with an "all" selection still plans both, so the zero
	// result above is the selection talking and not a broken fixture.
	tasks, _ = planner.collectTasks(context.Background(), config.OpFix, Selection{Mode: SelectionAll})
	if len(tasks) != 2 {
		t.Fatalf("all selection planned %d tasks, want 2", len(tasks))
	}
}
