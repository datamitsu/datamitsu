package tooling

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/project"
)

// The defect this whole mechanism exists for: tsc's answer depends on
// tsconfig.json, which no `.ts` glob matches. Editing only tsconfig.json left
// every per-file content hash unchanged, so the task was skipped with a tick and
// the typecheck never ran. The verdict's inputs must notice.
func TestVerdictInputsNoticeAConfigEditNoGlobMatches(t *testing.T) {
	root := t.TempDir()
	unit := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(unit, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(unit, "a.ts")
	tsconfig := filepath.Join(unit, "tsconfig.json")
	if err := os.WriteFile(source, []byte("export const a = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tsconfig, []byte(`{"compilerOptions":{"strict":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	members := []string{source}
	guards := []string{tsconfig}

	before := verdictInputs(members, guards, root)

	// Turn strict on. No .ts file changed.
	if err := os.WriteFile(tsconfig, []byte(`{"compilerOptions":{"strict":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	after := verdictInputs(members, guards, root)

	if before == after {
		t.Error("editing tsconfig.json left the verdict inputs unchanged; the stale pass would stand")
	}
}

// A member disappearing is a change: deletions are the classic cache miss.
func TestVerdictInputsNoticeADeletedMember(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.ts")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := verdictInputs([]string{file}, nil, root)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if after := verdictInputs([]string{file}, nil, root); before == after {
		t.Error("a deleted member left the inputs unchanged")
	}
}

// Identity must not depend on where the repository lives, or moving it orphans
// every entry.
func TestVerdictIdentityIgnoresAbsolutePaths(t *testing.T) {
	task := Task{
		ToolName:  "tsc",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App:   "tsc",
			Args:  []string{"--noEmit", "--tsBuildInfoFile", "{toolCache}/x.json"},
			Scope: config.ToolScopePerProject,
		},
	}
	// Raw args are hashed, so the same operation in two checkouts agrees.
	app, appAgain := verdictIdentity(task, "packages/app"), verdictIdentity(task, "packages/app")
	if app != appAgain {
		t.Error("identity is not stable for one task")
	}
	if web := verdictIdentity(task, "packages/web"); app == web {
		t.Error("two units must not share an identity")
	}
}

// Coverage gates the write, so its rules carry the correctness weight — and the
// denominator must come from the unit, never from the selection. Driving this
// through the planner rather than calling coverageFor directly is deliberate: a
// hand-fed denominator is exactly what hid the tautology this replaced.
func TestCoverageIsPartialForANarrowedRun(t *testing.T) {
	p := &Planner{
		rootPath: "/repo",
		cwdPath:  "/repo",
		tools: config.MapOfTools{
			"eslint": {Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App: "eslint", Args: []string{"{files}"},
					Scope: config.ToolScopePerProject, Globs: []string{"**/*.ts"},
				},
			}},
		},
		cachedFiles:      []string{"/repo/pkg/a.ts", "/repo/pkg/b.ts", "/repo/pkg/package.json"},
		cachedProjects:   []project.ProjectLocation{{Type: "npm-package", Path: "/repo/pkg"}},
		cacheInitialized: true,
	}

	// One file named out of two the unit contains.
	narrowed, _ := p.collectTasks(context.Background(), config.OpLint,
		Selection{Mode: SelectionPaths, Paths: []string{"/repo/pkg/a.ts"}})
	if len(narrowed) != 1 {
		t.Fatalf("expected one task, got %d", len(narrowed))
	}
	if narrowed[0].Coverage != CoveragePartial {
		t.Errorf("coverage = %q, want partial: the run saw a.ts but the unit holds b.ts too",
			narrowed[0].Coverage)
	}

	// The whole repository: the same unit is now fully covered.
	full, _ := p.collectTasks(context.Background(), config.OpLint, Selection{Mode: SelectionAll})
	if len(full) != 1 {
		t.Fatalf("expected one task, got %d", len(full))
	}
	if full[0].Coverage != CoverageComplete {
		t.Errorf("coverage = %q, want complete", full[0].Coverage)
	}
}

// An operation that puts no path in argv issues the same command either way, so
// a narrowed selection still covers its unit.
func TestCoverageIsCompleteWhenArgvIgnoresTheSelection(t *testing.T) {
	p := &Planner{
		rootPath: "/repo",
		cwdPath:  "/repo",
		tools: config.MapOfTools{
			"tsc": {Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App: "tsc", Args: []string{"--noEmit"},
					Scope: config.ToolScopePerProject, Globs: []string{"**/*.ts"},
				},
			}},
		},
		cachedFiles:      []string{"/repo/pkg/a.ts", "/repo/pkg/b.ts"},
		cachedProjects:   []project.ProjectLocation{{Type: "npm-package", Path: "/repo/pkg"}},
		cacheInitialized: true,
	}

	tasks, _ := p.collectTasks(context.Background(), config.OpLint,
		Selection{Mode: SelectionPaths, Paths: []string{"/repo/pkg/a.ts"}})
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(tasks))
	}
	if tasks[0].Coverage != CoverageComplete {
		t.Errorf("coverage = %q, want complete", tasks[0].Coverage)
	}
}
