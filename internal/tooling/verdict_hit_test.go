package tooling

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

// A stored verdict must short-circuit the run without launching the tool, and
// the skipped result has to be shaped exactly like a real one: consumers key
// JSON-L on RelativeDir and print the scope badge only when Scope is set, so a
// hit that omitted them would emit a differently-shaped event for the same task.
func TestExecuteTaskHitsTheVerdictCache(t *testing.T) {
	root := t.TempDir()
	unitDir := filepath.Join(root, "pkg")
	member := filepath.Join(unitDir, "a.ts")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(member, []byte("export const a = 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := cache.NewCache(t.TempDir(), root, config.Config{Tools: config.MapOfTools{}}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	// A command that would fail loudly if it ever ran.
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"tsc": {Command: filepath.Join(root, "does-not-exist"), Args: nil},
	}}
	e := NewExecutor(root, false, false, appMgr, c)

	task := Task{
		ToolName:  "tsc",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App: "tsc", Args: []string{"--noEmit"},
			Scope: config.ToolScopePerProject,
		},
		ProjectPath: unitDir,
		UnitDir:     "pkg",
		UnitMembers: []string{member},
		Coverage:    CoverageComplete,
	}

	key, inputs, ok := e.verdictKeys(task)
	if !ok {
		t.Fatal("precondition: the verdict cache should apply to this task")
	}
	e.recordVerdict(task, key, inputs, ok)

	result := e.executeTask(context.Background(), task)

	if !result.Success {
		t.Fatalf("a cached verdict did not short-circuit the run: %v", result.Error)
	}
	if result.RelativeDir != "pkg" {
		t.Errorf("RelativeDir = %q, want %q — a hit must be shaped like a real run", result.RelativeDir, "pkg")
	}
	if result.Scope != config.ToolScopePerProject {
		t.Errorf("Scope = %q, want it carried through on a hit", result.Scope)
	}

	// Editing a member must invalidate the verdict, or the cache hides real work.
	if err := os.WriteFile(member, []byte("export const a = 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, after, _ := e.verdictKeys(task); after == inputs {
		t.Fatal("editing a member left the input vector unchanged")
	}
	if !c.ShouldRunVerdict(key, mustInputs(e, task), e.verdictTTL()) {
		t.Error("the tool stayed cached after one of its members changed")
	}
}

func mustInputs(e *Executor, task Task) string {
	_, inputs, _ := e.verdictKeys(task)
	return inputs
}

// The per-file cache records "this file passed", which is only a verdict when a
// file's result stands alone. For unit granularity it is not: tsc's answer
// depends on tsconfig.json, which no .ts glob matches, so an edit to it left
// every content hash unchanged and the whole task was skipped with a tick.
func TestFilterFilesByCacheSkipsNonFileGranularity(t *testing.T) {
	root := t.TempDir()
	c, err := cache.NewCache(t.TempDir(), root, config.Config{Tools: config.MapOfTools{}}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	e := NewExecutor(root, false, false, &mockAppManager{}, c)

	files := []string{filepath.Join(root, "a.ts"), filepath.Join(root, "b.ts")}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	unit := Task{
		ToolName:  "tsc",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App: "tsc", Args: []string{"--noEmit"},
			Scope: config.ToolScopePerProject,
		},
		Files: files,
	}
	// Mark both files as passed, the way a per-file tool would.
	for _, f := range files {
		if err := c.AfterLint(f, "tsc", true); err != nil {
			t.Fatal(err)
		}
	}

	if got := e.filterFilesByCache(unit); len(got) != len(files) {
		t.Errorf("filterFilesByCache dropped %d of %d files for a unit task; per-file "+
			"entries do not speak for a whole-unit verdict", len(files)-len(got), len(files))
	}

	perFile := unit
	perFile.OpConfig = config.ToolOperation{
		App: "prettier", Args: []string{"--write", "{file}"},
		Scope: config.ToolScopePerFile,
	}
	perFile.ToolName = "tsc"
	if got := e.filterFilesByCache(perFile); len(got) != 0 {
		t.Errorf("filterFilesByCache kept %v; a file-granularity task must honour the per-file cache", got)
	}
}
