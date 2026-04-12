package traverser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCollectGitignorePaths(t *testing.T) {
	tmpDir := t.TempDir()

	root := tmpDir
	subdir1 := filepath.Join(tmpDir, "a")
	subdir2 := filepath.Join(tmpDir, "a", "b")
	target := filepath.Join(tmpDir, "a", "b", "c")

	_ = os.MkdirAll(target, 0755)

	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(subdir1, ".gitignore"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(subdir2, ".gitignore"), []byte(""), 0644)

	paths := collectGitignorePaths(root, target)

	if len(paths) != 3 {
		t.Errorf("len(paths) = %d, want 3", len(paths))
	}

	expectedPaths := []string{
		filepath.Join(root, ".gitignore"),
		filepath.Join(subdir1, ".gitignore"),
		filepath.Join(subdir2, ".gitignore"),
	}

	for i, expected := range expectedPaths {
		if i >= len(paths) {
			t.Errorf("missing path at index %d", i)
			continue
		}
		if paths[i] != expected {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], expected)
		}
	}
}

func TestCollectGitignorePathsNoGitignores(t *testing.T) {
	tmpDir := t.TempDir()

	root := tmpDir
	target := filepath.Join(tmpDir, "a", "b", "c")

	_ = os.MkdirAll(target, 0755)

	paths := collectGitignorePaths(root, target)

	if len(paths) != 0 {
		t.Errorf("len(paths) = %d, want 0", len(paths))
	}
}

func TestCollectGitignorePathsSameRootAndTarget(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(""), 0644)

	paths := collectGitignorePaths(tmpDir, tmpDir)

	if len(paths) != 1 {
		t.Errorf("len(paths) = %d, want 1", len(paths))
	}

	expected := filepath.Join(tmpDir, ".gitignore")
	if paths[0] != expected {
		t.Errorf("paths[0] = %q, want %q", paths[0], expected)
	}
}

func TestCollectGitignorePathsPartialGitignores(t *testing.T) {
	tmpDir := t.TempDir()

	root := tmpDir
	target := filepath.Join(tmpDir, "a", "b")

	_ = os.MkdirAll(target, 0755)

	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644)

	paths := collectGitignorePaths(root, target)

	if len(paths) != 1 {
		t.Errorf("len(paths) = %d, want 1", len(paths))
	}
}

func TestGetGitRoot(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to initialize git repo: %v", err)
	}

	ctx := context.Background()

	root, err := GetGitRoot(ctx, tmpDir)
	if err != nil {
		t.Fatalf("GetGitRoot() error = %v", err)
	}

	if root == "" {
		t.Error("GetGitRoot() returned empty string")
	}

	absRoot, _ := filepath.EvalSymlinks(root)
	absTmpDir, _ := filepath.EvalSymlinks(tmpDir)

	if absRoot != absTmpDir {
		t.Errorf("GetGitRoot() = %q, want %q", absRoot, absTmpDir)
	}
}

func TestGetGitRootFromSubdir(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to initialize git repo: %v", err)
	}

	subdir := filepath.Join(tmpDir, "a", "b", "c")
	_ = os.MkdirAll(subdir, 0755)

	ctx := context.Background()

	root, err := GetGitRoot(ctx, subdir)
	if err != nil {
		t.Fatalf("GetGitRoot() error = %v", err)
	}

	absRoot, _ := filepath.EvalSymlinks(root)
	absTmpDir, _ := filepath.EvalSymlinks(tmpDir)

	if absRoot != absTmpDir {
		t.Errorf("GetGitRoot() = %q, want %q", absRoot, absTmpDir)
	}
}

func TestGetGitRootNotGitRepo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	_, err := GetGitRoot(ctx, tmpDir)
	if err == nil {
		t.Error("GetGitRoot() should return error for non-git directory")
	}
}

func isGitAvailable() bool {
	cmd := exec.Command("git", "--version")
	return cmd.Run() == nil
}
