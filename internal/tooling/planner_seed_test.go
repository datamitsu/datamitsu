package tooling

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/trace"
	"github.com/datamitsu/datamitsu/internal/traverser"
)

// seedFixture is a small tree with two projects, so cache initialization has a
// file list, a project detection and a .datamitsuignore to derive from.
func seedFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"packages/api", "packages/web"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch(t, filepath.Join(root, "packages", "api", "package.json"))
	touch(t, filepath.Join(root, "packages", "api", "index.ts"))
	touch(t, filepath.Join(root, "packages", "web", "package.json"))
	touch(t, filepath.Join(root, "packages", "web", "app.tsx"))
	touch(t, filepath.Join(root, "main.go"))
	if err := os.WriteFile(filepath.Join(root, ".datamitsuignore"), []byte("**/*.tsx: prettier\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// walksDuring reports how many repository walks fn performed.
func walksDuring(t *testing.T, fn func()) int64 {
	t.Helper()
	prev := trace.Enabled()
	trace.Reset()
	trace.SetEnabled(true)
	defer func() {
		trace.SetEnabled(prev)
		trace.Reset()
	}()

	fn()

	for _, c := range trace.Counters() {
		if c.Name() == "walk.repository_walks" {
			return c.Value()
		}
	}
	t.Fatal("counter walk.repository_walks is not registered")
	return 0
}

// A seeded planner must be indistinguishable from one that walked for itself.
//
// The seed exists only to avoid a second traversal of a tree the caller already
// traversed, so the whole of its contract is that everything initialization
// derives comes out identical — the file list, its root-relative form, the
// detected projects and the ignore matcher built from them. Anything that
// differs here reaches the plan.
func TestSeedFilesMatchesWalk(t *testing.T) {
	ctx := context.Background()
	root := seedFixture(t)

	walked := NewPlanner(root, root, nil, nil, nil, nil)
	if err := walked.initializeCache(ctx); err != nil {
		t.Fatal(err)
	}

	files, err := traverser.FindFilesFromPath(ctx, root, root)
	if err != nil {
		t.Fatal(err)
	}
	seeded := NewPlanner(root, root, nil, nil, nil, nil)
	seeded.SeedFiles(files)

	seedWalks := walksDuring(t, func() {
		if err := seeded.initializeCache(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if seedWalks != 0 {
		t.Errorf("seeded initialization performed %d repository walks, want 0", seedWalks)
	}

	if !reflect.DeepEqual(seeded.cachedFiles, walked.cachedFiles) {
		t.Errorf("cachedFiles differ:\n seeded %v\n walked %v", seeded.cachedFiles, walked.cachedFiles)
	}
	if !reflect.DeepEqual(seeded.cachedRel, walked.cachedRel) {
		t.Errorf("cachedRel differ:\n seeded %v\n walked %v", seeded.cachedRel, walked.cachedRel)
	}
	if !reflect.DeepEqual(seeded.cachedProjects, walked.cachedProjects) {
		t.Errorf("cachedProjects differ:\n seeded %v\n walked %v", seeded.cachedProjects, walked.cachedProjects)
	}

	// The matcher is built from the .datamitsuignore files the list yields, which
	// is the half of the seed that decides whether a tool runs on a path.
	rel := filepath.Join("packages", "web", "app.tsx")
	got, want := seeded.ignoreMatcher.IsDisabled("prettier", rel), walked.ignoreMatcher.IsDisabled("prettier", rel)
	if got != want {
		t.Errorf("ignoreMatcher disagrees on %s: seeded %v, walked %v", rel, got, want)
	}
	if !want {
		t.Fatalf("the fixture rule never applied to %s, so the comparison proved nothing", rel)
	}
}

// Invalidate exists because a file list can age out of date. An unconsumed seed
// is such a list, so it must not survive to be handed to the re-initialization
// that Invalidate asked for.
func TestInvalidateDropsAnUnconsumedSeed(t *testing.T) {
	ctx := context.Background()
	root := seedFixture(t)

	p := NewPlanner(root, root, nil, nil, nil, nil)
	p.SeedFiles([]string{filepath.Join(root, "main.go")}) // deliberately partial
	p.Invalidate()

	walks := walksDuring(t, func() {
		if err := p.initializeCache(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if walks != 1 {
		t.Errorf("initialization after Invalidate performed %d walks, want 1 — the stale seed was used", walks)
	}
	if len(p.cachedFiles) < 2 {
		t.Errorf("cachedFiles = %v, want the full tree rather than the dropped seed", p.cachedFiles)
	}
}

// A planner with no seed still has to walk, and a walk that fails must surface
// as an error rather than as an empty repository — an empty file list plans no
// tasks, which reads as a clean run.
func TestInitializeCacheReportsAWalkFailure(t *testing.T) {
	p := NewPlanner(filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir(), nil, nil, nil, nil)

	err := p.initializeCache(context.Background())
	if err == nil {
		t.Fatal("initializeCache() = nil, want the walk failure")
	}
	if p.cacheInitialized {
		t.Error("the cache was marked initialized despite the failed walk")
	}
}

// A seed is consumed once. A planner that has already initialized holds the
// list the seed would have produced, so a later seed is not a way to swap the
// file set out from under a cache that is already derived from it.
func TestSeedFilesIsIgnoredOnceInitialized(t *testing.T) {
	ctx := context.Background()
	root := seedFixture(t)

	p := NewPlanner(root, root, nil, nil, nil, nil)
	if err := p.initializeCache(ctx); err != nil {
		t.Fatal(err)
	}
	before := append([]string(nil), p.cachedFiles...)

	p.SeedFiles([]string{filepath.Join(root, "main.go")})
	if err := p.initializeCache(ctx); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(p.cachedFiles, before) {
		t.Errorf("cachedFiles = %v, want them unchanged at %v", p.cachedFiles, before)
	}
}
