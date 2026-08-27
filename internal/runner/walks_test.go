package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/trace"
)

// unformattedRule is the fixture's .datamitsuignore content, deliberately not in
// canonical form: whether `fix` rewrote it is how a test tells that the hoisted
// discovery result actually reached RunFix, rather than an empty list reaching it
// and the walk count landing on the same number for the wrong reason.
const (
	unformattedRule = "*.go:golangci-lint  \n"
	canonicalRule   = "*.go: golangci-lint\n"
)

// walkFixture is a repository with one .datamitsuignore, so bundled discovery has
// something to find and the walk it needs is not optimized away.
func walkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, ".datamitsuignore"), []byte(unformattedRule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

// ignoreFileContent reads the fixture's .datamitsuignore back.
func ignoreFileContent(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".datamitsuignore"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runWithWalkCount(t *testing.T, ops []config.OperationType) int64 {
	t.Helper()
	prev := trace.Enabled()
	trace.Reset()
	trace.SetEnabled(true)
	t.Cleanup(func() {
		trace.SetEnabled(prev)
		trace.Reset()
	})

	err := RunSequential(ops, nil, "", false, "", false, Options{},
		func() (*config.Config, string, error) {
			return &config.Config{
				Tools:        config.MapOfTools{"golangci-lint": {Name: "golangci-lint"}},
				ProjectTypes: config.MapOfProjectTypes{},
			}, "", nil
		},
	)
	if err != nil {
		t.Fatalf("RunSequential(%v) error = %v", ops, err)
	}

	for _, c := range trace.Counters() {
		if c.Name() == "walk.repository_walks" {
			return c.Value()
		}
	}
	t.Fatal("counter walk.repository_walks is not registered")
	return 0
}

// A run walks the repository once, whatever it was asked to do.
//
// Three consumers want that list: bundled fix, bundled lint and the planner.
// Each used to walk for itself, so a `check` paid for three full gitignore-aware
// traversals of one tree and a `lint` for two — every one of them producing the
// same answer. bundled's two were merged first; the planner now takes the same
// list by seed rather than walking again a few milliseconds later.
//
// The count alone is a weak assertion — handing every consumer an empty list
// would also read as one walk — so the fix is checked to have rewritten the file
// it was given. The runner passes one variable to both consumers, and that the
// planner's copy is as good as its own walk is TestSeedFilesMatchesWalk.
func TestRunPerformsOneRepositoryWalk(t *testing.T) {
	lintRoot := walkFixture(t)
	lintWalks := runWithWalkCount(t, []config.OperationType{config.OpLint})
	if got := ignoreFileContent(t, lintRoot); got != unformattedRule {
		t.Errorf("lint rewrote .datamitsuignore to %q; lint must not fix", got)
	}

	checkRoot := walkFixture(t)
	checkWalks := runWithWalkCount(t, []config.OperationType{config.OpFix, config.OpLint})
	if got := ignoreFileContent(t, checkRoot); got != canonicalRule {
		t.Errorf("check left .datamitsuignore as %q, want %q — the discovered files never reached RunFix", got, canonicalRule)
	}

	if lintWalks != 1 {
		t.Errorf("lint performed %d repository walks, want 1", lintWalks)
	}
	if checkWalks != 1 {
		t.Errorf("check performed %d repository walks, want 1", checkWalks)
	}
}
