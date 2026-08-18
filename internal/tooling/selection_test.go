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

// The reported bug: a repository-scoped formatter asked to fix one file from a
// subdirectory used to be dropped without a trace — not planned, not skipped,
// absent from every report. What decides is whether its verdict survives
// narrowing, which is granularity, not scope.
func TestCollectTasksNarrowableRepositoryToolRunsFromSubdirectory(t *testing.T) {
	newPlanner := func() *Planner {
		return &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo/packages/api",
			tools: config.MapOfTools{
				// A file list in argv: narrowing keeps the answer complete.
				"oxfmt": {Operations: map[config.OperationType]config.ToolOperation{
					config.OpFix: {
						App: "oxfmt", Args: []string{"--write", "{files}"},
						Scope: config.ToolScopeRepository, Globs: []string{"**/*.json"},
					},
				}},
				// No file list: the verdict is about the repository as a whole.
				"syncpack": {Operations: map[config.OperationType]config.ToolOperation{
					config.OpFix: {
						App: "syncpack", Args: []string{"fix"},
						Scope: config.ToolScopeRepository, Globs: []string{"**/package.json"},
					},
				}},
			},
			cachedFiles: []string{
				"/repo/packages/api/swagger.json",
				"/repo/packages/web/other.json",
				"/repo/package.json",
			},
			cacheInitialized: true,
		}
	}

	sel := Selection{Mode: SelectionPaths, Paths: []string{"/repo/packages/api/swagger.json"}}
	tasks, skipped := newPlanner().collectTasks(context.Background(), config.OpFix, sel)

	if len(tasks) != 1 || tasks[0].ToolName != "oxfmt" {
		t.Fatalf("expected oxfmt to be planned, got %+v", tasks)
	}
	// The process still starts at the git root, so {root}-anchored config paths
	// and {cwd} keep resolving to what they resolved to before.
	if tasks[0].ProjectPath != "/repo" {
		t.Errorf("ProjectPath = %q, want the git root", tasks[0].ProjectPath)
	}
	if len(tasks[0].Files) != 1 || tasks[0].Files[0] != "/repo/packages/api/swagger.json" {
		t.Errorf("Files = %v, want only the named file", tasks[0].Files)
	}

	if len(skipped) != 1 || skipped[0].ToolName != "syncpack" {
		t.Fatalf("expected syncpack to be reported as skipped, got %+v", skipped)
	}
	if skipped[0].Reason != SkipReasonNotNarrowable {
		t.Errorf("reason = %v, want not-narrowable", skipped[0].Reason)
	}
}

// widenTo: "repo" is the opt-in that runs the whole-repository tools anyway.
func TestCollectTasksWidenToRepoRunsNonNarrowableTools(t *testing.T) {
	p := &Planner{
		rootPath: "/repo",
		cwdPath:  "/repo/packages/api",
		tools: config.MapOfTools{
			"syncpack": {Operations: map[config.OperationType]config.ToolOperation{
				config.OpFix: {
					App: "syncpack", Args: []string{"fix"},
					Scope: config.ToolScopeRepository, Globs: []string{"**/package.json"},
				},
			}},
		},
		cachedFiles:      []string{"/repo/package.json"},
		cacheInitialized: true,
	}
	p.SetWidenPolicy(&config.Execution{
		WidenTo: map[config.OperationType]config.WidenTo{config.OpFix: config.WidenToRepo},
	}, "")

	tasks, skipped := p.collectTasks(context.Background(), config.OpFix, Selection{Mode: SelectionSubtree, Dir: "/repo/packages/api"})
	if len(tasks) != 1 {
		t.Fatalf("widenTo repo should run it, got %d tasks and %+v", len(tasks), skipped)
	}
}
