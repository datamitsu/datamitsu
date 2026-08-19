package tooling

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func touch(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasGuard(guards []string, path string) bool {
	return slices.Contains(guards, path)
}

// A unit inherits whatever its ancestors declare, so the guard list has to walk
// up to the git root. Without it, editing the root tsconfig.json leaves every
// package's stored verdict looking valid.
func TestUnitGuardsWalkToTheRoot(t *testing.T) {
	root := t.TempDir()
	unit := filepath.Join(root, "packages", "api")
	rootCfg := touch(t, filepath.Join(root, "tsconfig.json"))
	unitCfg := touch(t, filepath.Join(unit, "tsconfig.json"))
	unrelated := touch(t, filepath.Join(root, "packages", "web", "tsconfig.json"))

	p := &Planner{rootPath: root, cwdPath: unit}
	guards := p.unitGuards(Task{OpConfig: config.ToolOperation{App: "tsc"}}, unit)

	if !hasGuard(guards, rootCfg) {
		t.Error("the root config is missing; editing it would not invalidate the unit's verdict")
	}
	if !hasGuard(guards, unitCfg) {
		t.Error("the unit's own config is missing")
	}
	if hasGuard(guards, unrelated) {
		t.Error("a sibling's config leaked in; every unrelated edit would cause a miss")
	}
	if !slices.IsSorted(guards) {
		t.Errorf("guards are unsorted: %v", guards)
	}
}

// Only files count. A directory that happens to share a guard name would
// otherwise hash to the "(missing)" sentinel forever.
func TestUnitGuardsIgnoreDirectoriesAndAbsentFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "package.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := &Planner{rootPath: root, cwdPath: root}
	guards := p.unitGuards(Task{OpConfig: config.ToolOperation{App: "eslint"}}, root)

	if hasGuard(guards, filepath.Join(root, "package.json")) {
		t.Error("a directory was recorded as a guard")
	}
	if hasGuard(guards, filepath.Join(root, "go.mod")) {
		t.Error("a file that does not exist was recorded as a guard")
	}
}

// Anything the args point at is a declared input by construction.
func TestUnitGuardsIncludeArgumentPaths(t *testing.T) {
	root := t.TempDir()
	cfg := touch(t, filepath.Join(root, "custom", "sqruff.toml"))

	p := &Planner{rootPath: root, cwdPath: root}
	task := Task{OpConfig: config.ToolOperation{
		App:  "sqruff",
		Args: []string{"--config", "{root}/custom/sqruff.toml", "lint"},
	}}
	guards := p.unitGuards(task, root)

	if !hasGuard(guards, cfg) {
		t.Errorf("guards = %v, want the config named in the args", guards)
	}
}

// The tool cache is an *output*. Folding an output into a precondition
// guarantees a permanent miss: the tool rewrites it, so the next run never
// matches the hash the previous one stored.
func TestUnitGuardsExcludeTheToolCache(t *testing.T) {
	root := t.TempDir()
	out := touch(t, filepath.Join(root, ".datamitsu-cache", "tsbuildinfo"))

	p := &Planner{rootPath: root, cwdPath: root}
	task := Task{OpConfig: config.ToolOperation{
		App:  "tsc",
		Args: []string{"--tsBuildInfoFile", "{root}/.datamitsu-cache/tsbuildinfo"},
	}}

	if guards := p.unitGuards(task, root); hasGuard(guards, out) {
		t.Error("the tool cache was recorded as an input; the verdict could never hit again")
	}
}

// invalidateOn resolves against the unit *and* every ancestor: in a monorepo the
// config a package depends on usually lives above it, and without the ancestor
// walk a package could not name it at all.
func TestUnitGuardsResolveInvalidateOnAgainstAncestors(t *testing.T) {
	root := t.TempDir()
	unit := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(unit, 0o755); err != nil {
		t.Fatal(err)
	}
	shared := touch(t, filepath.Join(root, "shared.config.json"))

	p := &Planner{rootPath: root, cwdPath: unit}
	task := Task{OpConfig: config.ToolOperation{
		App:          "eslint",
		InvalidateOn: []string{"shared.config.json"},
	}}

	if guards := p.unitGuards(task, unit); !hasGuard(guards, shared) {
		t.Errorf("guards = %v, want the ancestor config named by invalidateOn", guards)
	}
}

func TestUnitMembers(t *testing.T) {
	root := filepath.FromSlash("/repo")
	unit := filepath.Join(root, "packages", "api")
	files := []string{
		filepath.Join(unit, "a.ts"),
		filepath.Join(unit, "nested", "b.ts"),
		filepath.Join(root, "packages", "web", "c.ts"),
		filepath.Join(root, "root.ts"),
	}

	t.Run("a subdirectory takes only its own files", func(t *testing.T) {
		p := &Planner{rootPath: root, cachedFiles: files, cacheInitialized: true}
		got := p.unitMembers(unit)
		if len(got) != 2 {
			t.Fatalf("unitMembers = %v, want the two files under the unit", got)
		}
	})

	t.Run("the root takes everything", func(t *testing.T) {
		p := &Planner{rootPath: root, cachedFiles: files, cacheInitialized: true}
		if got := p.unitMembers(root); len(got) != len(files) {
			t.Errorf("unitMembers = %v, want all %d files", got, len(files))
		}
	})

	// Without a walk there is no membership, and an empty member list makes the
	// input vector constant — a permanent, unearned pass.
	t.Run("no walk means no members", func(t *testing.T) {
		p := &Planner{rootPath: root, cachedFiles: files}
		if got := p.unitMembers(unit); got != nil {
			t.Errorf("unitMembers = %v, want nil when the file walk has not run", got)
		}
	})
}

func TestPlannerContains(t *testing.T) {
	p := &Planner{}
	dir := filepath.FromSlash("/repo/svc/a")

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(dir, "main.go"), true},
		{dir, true},
		{filepath.FromSlash("/repo/svc/ab/main.go"), false},
		{filepath.FromSlash("/repo/svc"), false},
	}

	for _, tt := range tests {
		if got := p.contains(dir, tt.path); got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", dir, tt.path, got, tt.want)
		}
	}
}
