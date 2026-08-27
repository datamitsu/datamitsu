package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/trace"
)

// walkFixture is a repository with one .datamitsuignore, so bundled discovery has
// something to find and the walk it needs is not optimized away.
func walkFixture(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, ".datamitsuignore"), []byte("*.go: golangci-lint\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
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

// A `check` is a fix followed by a lint over the same tree. Both used to discover
// the .datamitsuignore files themselves, so the run paid for one full
// gitignore-aware repository walk more than a `lint` did — for an identical
// answer. Discovery now happens once and is handed to both.
func TestCheckWalksTheRepositoryTwice(t *testing.T) {
	walkFixture(t)
	lintWalks := runWithWalkCount(t, []config.OperationType{config.OpLint})

	walkFixture(t)
	checkWalks := runWithWalkCount(t, []config.OperationType{config.OpFix, config.OpLint})

	if lintWalks != 2 {
		t.Errorf("lint performed %d repository walks, want 2 (bundled discovery + the planner)", lintWalks)
	}
	if checkWalks != 2 {
		t.Errorf("check performed %d repository walks, want 2 — fix and lint must share one discovery", checkWalks)
	}
}
