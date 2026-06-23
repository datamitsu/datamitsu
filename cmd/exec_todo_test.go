package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates a git repo in a fresh temp dir and returns its
// symlink-resolved root. Skips the test if git is unavailable.
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = resolved
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
	return resolved
}

func TestGetGitRoot(t *testing.T) {
	root := initGitRepo(t)

	// Run from a nested subdirectory; GetGitRoot resolves via $PWD.
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	got, err := GetGitRoot(context.Background())
	if err != nil {
		t.Fatalf("GetGitRoot() error = %v", err)
	}
	resolvedGot, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedGot != root {
		t.Errorf("GetGitRoot() = %q, want %q", resolvedGot, root)
	}
}

func TestGetGitRootOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// A temp dir with no git repo anywhere up the tree.
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := GetGitRoot(context.Background()); err == nil {
		t.Error("GetGitRoot() outside a repo = nil error, want error")
	}
}

func TestCollectGitignorePathsAndRules(t *testing.T) {
	root := initGitRepo(t)
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Root .gitignore ignores *.log; nested one ignores build/.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n# comment\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".gitignore"), []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := collectGitignorePaths(root, sub)
	if len(paths) != 2 {
		t.Fatalf("collectGitignorePaths() = %v, want 2 paths", paths)
	}

	gm, err := CollectGitignoreRules(context.Background(), root, sub)
	if err != nil {
		t.Fatalf("CollectGitignoreRules() error = %v", err)
	}

	tests := []struct {
		name  string
		path  string
		isDir bool
		want  bool
	}{
		{"root rule matches log", "debug.log", false, true},
		{"root rule matches nested log", "pkg/x.log", false, true},
		{"nested rule matches build dir", "pkg/build", true, true},
		{"unmatched file", "pkg/main.go", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gm.IsIgnored(tt.path, tt.isDir); got != tt.want {
				t.Errorf("IsIgnored(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
			}
		})
	}

	t.Run("absolute path is relativized", func(t *testing.T) {
		if !gm.IsIgnored(filepath.Join(root, "x.log"), false) {
			t.Error("absolute *.log path should be ignored")
		}
	})
}

func TestCollectGitignoreRulesTargetOutsideRoot(t *testing.T) {
	// target not under root → empty matcher, nothing ignored.
	gm, err := CollectGitignoreRules(context.Background(), "/repo", "/elsewhere")
	if err != nil {
		t.Fatalf("CollectGitignoreRules() error = %v", err)
	}
	if gm.IsIgnored("anything.log", false) {
		t.Error("empty matcher should not ignore anything")
	}
}

func TestGetGitignoreMatcher(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	gm, err := GetGitignoreMatcher(context.Background())
	if err != nil {
		t.Fatalf("GetGitignoreMatcher() error = %v", err)
	}
	if !gm.IsIgnored("scratch.tmp", false) {
		t.Error("*.tmp should be ignored by the cwd matcher")
	}
}
