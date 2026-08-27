package tooling

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The verdict input vector for a unit is members + guards. Plan resets only the
// guard half; the member half comes from the walk, which initializeCache does
// once. A planner that outlives a single run therefore needs an explicit way to
// drop the walk, or a file created mid-session leaves the inputs unchanged and
// the tool is skipped for a unit whose contents changed.
func TestInvalidateReWalksSoNewFilesReachUnitMembers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	unit := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(unit, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(unit, "a.ts"))

	p := NewPlanner(root, root, nil, nil, nil, nil)
	if err := p.initializeCache(ctx); err != nil {
		t.Fatal(err)
	}
	if got := p.unitMembers(unit); len(got) != 1 {
		t.Fatalf("unitMembers = %v, want the single file the walk found", got)
	}

	newFile := touch(t, filepath.Join(unit, "b.ts"))

	// Without Invalidate the walk is frozen: initializeCache early-returns.
	if err := p.initializeCache(ctx); err != nil {
		t.Fatal(err)
	}
	if got := p.unitMembers(unit); len(got) != 1 {
		t.Fatalf("unitMembers = %v, want the stale list until the planner is invalidated", got)
	}

	p.Invalidate()
	if err := p.initializeCache(ctx); err != nil {
		t.Fatal(err)
	}
	got := p.unitMembers(unit)
	if !slices.Contains(got, newFile) {
		t.Errorf("unitMembers = %v, want the file created after the first walk", got)
	}
}

// Invalidate must drop every memo derived from the walk, not just the file list:
// a survivor keeps answering from the walk that is gone.
func TestInvalidateDropsEveryWalkDerivedCache(t *testing.T) {
	root := t.TempDir()
	p := NewPlanner(root, root, nil, nil, nil, nil)
	p.cachedFiles = []string{filepath.Join(root, "a.ts")}
	p.cachedRel = []string{"a.ts"}
	p.cachedProjects = nil
	p.cacheInitialized = true
	p.globCache = map[string][]string{"*.ts": p.cachedFiles}
	p.memberCache = map[string][]string{root: p.cachedFiles}
	p.guardCache = map[string][]string{root: nil}

	p.Invalidate()

	if p.cacheInitialized || p.cachedFiles != nil || p.cachedRel != nil {
		t.Error("the file walk survived Invalidate")
	}
	if p.globCache != nil || p.memberCache != nil || p.guardCache != nil {
		t.Error("a walk-derived memo survived Invalidate")
	}
	if p.ignoreMatcher != nil {
		t.Error("the .datamitsuignore matcher survived Invalidate")
	}
}
