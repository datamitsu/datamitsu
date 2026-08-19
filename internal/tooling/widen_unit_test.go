package tooling

import (
	"context"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/project"
)

// noArgvPathPlanner has two sibling modules and one per-project fix operation
// whose argv carries no path — the shape of `go fmt ./...`, `golangci-lint run`
// and every other formatter that finds its own files. Those rewrite in place.
func noArgvPathPlanner(cwd string) *Planner {
	return &Planner{
		rootPath: "/repo",
		cwdPath:  cwd,
		tools: config.MapOfTools{
			"gofmt": {Operations: map[config.OperationType]config.ToolOperation{
				config.OpFix: {
					App: "gofmt", Args: []string{"fmt", "./..."},
					Scope: config.ToolScopePerProject,
				},
			}},
		},
		cachedFiles:      []string{"/repo/svc/a/main.go", "/repo/svc/b/main.go"},
		cachedProjects:   []project.ProjectLocation{{Path: "/repo/svc/a"}, {Path: "/repo/svc/b"}},
		cacheInitialized: true,
	}
}

func projectPaths(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ProjectPath)
	}
	return out
}

// Naming one file used to plan the formatter in every module, because a
// glob-less operation takes the "run once per project" path and nothing
// consulted the widening policy there. The tool then rewrote files in modules
// the user never mentioned — a silent widening, and the widening is what edits.
func TestCollectTasksHonoursWidenToForGlobLessUnitTools(t *testing.T) {
	sel := Selection{Mode: SelectionPaths, Paths: []string{"/repo/svc/a/main.go"}}

	t.Run("the default policy reaches the unit holding the file, and no further", func(t *testing.T) {
		p := noArgvPathPlanner("/repo")
		tasks, skipped := p.collectTasks(context.Background(), config.OpFix, sel)

		if got := projectPaths(tasks); len(got) != 1 || got[0] != "/repo/svc/a" {
			t.Errorf("planned %v, want only the unit holding the named file", got)
		}
		if len(skipped) != 0 {
			t.Errorf("skipped %+v; the tool answered the question that was asked", skipped)
		}
	})

	t.Run("target refuses to run it at all", func(t *testing.T) {
		p := noArgvPathPlanner("/repo")
		p.SetWidenPolicy(nil, config.WidenToTarget)
		tasks, skipped := p.collectTasks(context.Background(), config.OpFix, sel)

		if len(tasks) != 0 {
			t.Errorf("planned %v; a whole unit is more than what was named", projectPaths(tasks))
		}
		if len(skipped) != 1 || skipped[0].Reason != SkipReasonNotNarrowable {
			t.Fatalf("skipped %+v, want one not-narrowable entry — refusing silently is the "+
				"defect this model exists to remove", skipped)
		}
	})

	t.Run("repo runs it everywhere", func(t *testing.T) {
		p := noArgvPathPlanner("/repo")
		p.SetWidenPolicy(nil, config.WidenToRepo)
		tasks, _ := p.collectTasks(context.Background(), config.OpFix, sel)

		if len(tasks) != 2 {
			t.Errorf("planned %v, want both modules", projectPaths(tasks))
		}
	})
}

// argv that carries the file list is already narrowed by that list, so the
// policy has nothing to add and every unit with matching files still runs.
func TestCollectTasksLeavesFileCarryingArgsAlone(t *testing.T) {
	p := noArgvPathPlanner("/repo")
	p.tools = config.MapOfTools{
		"prettier": {Operations: map[config.OperationType]config.ToolOperation{
			config.OpFix: {
				App: "prettier", Args: []string{"--write", "{files}"},
				Scope: config.ToolScopePerProject, Globs: []string{"**/*.go"},
			},
		}},
	}
	p.SetWidenPolicy(nil, config.WidenToTarget)

	tasks, skipped := p.collectTasks(context.Background(), config.OpFix,
		Selection{Mode: SelectionPaths, Paths: []string{"/repo/svc/a/main.go"}})

	if len(tasks) != 1 || tasks[0].ProjectPath != "/repo/svc/a" {
		t.Fatalf("planned %v, want the named file's unit", projectPaths(tasks))
	}
	if len(skipped) != 0 {
		t.Errorf("skipped %+v; the file list already narrows this tool", skipped)
	}
}

// Standing in a directory targets that subtree: units inside it run, and under
// the default policy the unit you are standing in runs too.
func TestCollectTasksHonoursWidenToForASubtree(t *testing.T) {
	sel := Selection{Mode: SelectionSubtree, Dir: "/repo/svc/a"}

	t.Run("default reaches the surrounding unit", func(t *testing.T) {
		p := noArgvPathPlanner("/repo/svc/a")
		tasks, _ := p.collectTasks(context.Background(), config.OpFix, sel)

		if got := projectPaths(tasks); len(got) != 1 || got[0] != "/repo/svc/a" {
			t.Errorf("planned %v, want the unit being stood in", got)
		}
	})

	t.Run("target still allows a unit that sits inside the target", func(t *testing.T) {
		p := noArgvPathPlanner("/repo/svc")
		p.SetWidenPolicy(nil, config.WidenToTarget)
		tasks, _ := p.collectTasks(context.Background(), config.OpFix,
			Selection{Mode: SelectionSubtree, Dir: "/repo/svc"})

		if len(tasks) != 2 {
			t.Errorf("planned %v, want both units — they are inside what was targeted",
				projectPaths(tasks))
		}
	})
}

// A whole-repository run is the common case and must keep planning everything.
func TestCollectTasksPlansEveryUnitForAWholeRepositoryRun(t *testing.T) {
	p := noArgvPathPlanner("/repo")
	p.SetWidenPolicy(nil, config.WidenToTarget)

	tasks, skipped := p.collectTasks(context.Background(), config.OpFix, Selection{Mode: SelectionAll})

	if len(tasks) != 2 {
		t.Errorf("planned %v, want both units", projectPaths(tasks))
	}
	if len(skipped) != 0 {
		t.Errorf("skipped %+v on a whole-repository run", skipped)
	}
}
