package tooling

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
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

// Coverage is what gates the write, so its rules carry the correctness weight.
func TestCoverageFor(t *testing.T) {
	unit := config.ToolOperation{Scope: config.ToolScopePerProject}
	withFiles := func(op config.ToolOperation, args []string, files ...string) Task {
		op.Args = args
		return Task{OpConfig: op, Files: files}
	}

	// argv does not depend on the selection, so the command is the one a full run
	// would have issued.
	if got := coverageFor(withFiles(unit, []string{"run"}), Selection{Mode: SelectionPaths}, []string{"a", "b"}); got != CoverageComplete {
		t.Errorf("arity none = %q, want complete", got)
	}
	// A narrowed file list covers less than the unit.
	if got := coverageFor(withFiles(unit, []string{"{files}"}, "a"), Selection{Mode: SelectionPaths}, []string{"a", "b"}); got != CoveragePartial {
		t.Errorf("narrowed file list = %q, want partial", got)
	}
	// The whole repository was asked for, so whatever matched is the whole unit.
	if got := coverageFor(withFiles(unit, []string{"{files}"}, "a"), Selection{Mode: SelectionAll}, []string{"a"}); got != CoverageComplete {
		t.Errorf("full selection = %q, want complete", got)
	}
}
