package traverser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// relFiles walks root and returns the found files relative to it.
func relFiles(t *testing.T, root string) []string {
	t.Helper()
	files, err := FindFiles(context.Background(), root)
	if err != nil {
		t.Fatalf("FindFiles() error = %v", err)
	}
	rel := make([]string, 0, len(files))
	for _, f := range files {
		r, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, filepath.ToSlash(r))
	}
	return rel
}

func initRepo(t *testing.T) string {
	t.Helper()
	if !isGitAvailable() {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	return root
}

func TestFindFilesHonorsInfoExclude(t *testing.T) {
	root := initRepo(t)
	// Anchored to the root even though the file lives in .git/info.
	writeFile(t, filepath.Join(root, ".git", "info", "exclude"), "/local.txt\n*.log\n")
	writeFile(t, filepath.Join(root, ".gitignore"), "!keep.log\n")
	for _, name := range []string{"local.txt", "sub/local.txt", "debug.log", "keep.log", "file.txt"} {
		writeFile(t, filepath.Join(root, name), "")
	}

	got := relFiles(t, root)
	want := []string{".gitignore", "file.txt", "keep.log", "sub/local.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("FindFiles() = %v, want %v", got, want)
	}
}

func TestFindFilesHonorsMainInfoExcludeInLinkedWorktree(t *testing.T) {
	main := initRepo(t)
	runGit(t, main, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	writeFile(t, filepath.Join(main, ".git", "info", "exclude"), "scratch/\n")

	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, main, "worktree", "add", "-q", wt)
	writeFile(t, filepath.Join(wt, "scratch", "notes.txt"), "")
	writeFile(t, filepath.Join(wt, "file.txt"), "")

	// The worktree's own .git file is not what this test is about.
	got := slices.DeleteFunc(relFiles(t, wt), func(f string) bool { return f == ".git" })
	want := []string{"file.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("FindFiles() = %v, want %v", got, want)
	}
}

func TestFindFilesHonorsCoreExcludesFile(t *testing.T) {
	root := initRepo(t)
	excludes := filepath.Join(t.TempDir(), "ignore")
	writeFile(t, excludes, "*.bak\nlocal.txt\n")
	runGit(t, root, "config", "core.excludesFile", excludes)
	// info/exclude outranks core.excludesFile.
	writeFile(t, filepath.Join(root, ".git", "info", "exclude"), "!local.txt\n")
	for _, name := range []string{"a.bak", "local.txt", "file.txt"} {
		writeFile(t, filepath.Join(root, name), "")
	}

	got := relFiles(t, root)
	want := []string{"file.txt", "local.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("FindFiles() = %v, want %v", got, want)
	}
}

func TestFindFilesHonorsDefaultXDGExcludesFile(t *testing.T) {
	root := initRepo(t)
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	writeFile(t, filepath.Join(configHome, "git", "ignore"), "*.bak\n")
	writeFile(t, filepath.Join(root, "a.bak"), "")
	writeFile(t, filepath.Join(root, "file.txt"), "")

	got := relFiles(t, root)
	want := []string{"file.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("FindFiles() = %v, want %v", got, want)
	}
}

func TestFindFilesEmptyCoreExcludesFileDisablesDefault(t *testing.T) {
	root := initRepo(t)
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	writeFile(t, filepath.Join(configHome, "git", "ignore"), "*.bak\n")
	runGit(t, root, "config", "core.excludesFile", "")
	writeFile(t, filepath.Join(root, "a.bak"), "")

	got := relFiles(t, root)
	want := []string{"a.bak"}
	if !slices.Equal(got, want) {
		t.Errorf("FindFiles() = %v, want %v", got, want)
	}
}

func TestGitCommonDir(t *testing.T) {
	t.Run("no .git", func(t *testing.T) {
		if got := gitCommonDir(t.TempDir()); got != "" {
			t.Errorf("gitCommonDir() = %q, want empty", got)
		}
	})

	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		_ = os.MkdirAll(filepath.Join(root, ".git"), 0o755)
		if got, want := gitCommonDir(root), filepath.Join(root, ".git"); got != want {
			t.Errorf("gitCommonDir() = %q, want %q", got, want)
		}
	})

	t.Run("relative gitdir file with commondir", func(t *testing.T) {
		base := t.TempDir()
		root := filepath.Join(base, "wt")
		gitDir := filepath.Join(base, "main", ".git", "worktrees", "wt")
		writeFile(t, filepath.Join(root, ".git"), "gitdir: ../main/.git/worktrees/wt\n")
		writeFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
		if got, want := gitCommonDir(root), filepath.Join(base, "main", ".git"); got != want {
			t.Errorf("gitCommonDir() = %q, want %q", got, want)
		}
	})

	t.Run("gitdir file without commondir", func(t *testing.T) {
		base := t.TempDir()
		root := filepath.Join(base, "sub")
		gitDir := filepath.Join(base, ".git", "modules", "sub")
		_ = os.MkdirAll(gitDir, 0o755)
		writeFile(t, filepath.Join(root, ".git"), "gitdir: "+gitDir+"\n")
		if got := gitCommonDir(root); got != gitDir {
			t.Errorf("gitCommonDir() = %q, want %q", got, gitDir)
		}
	})

	t.Run("malformed file", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git"), "not a gitdir line\n")
		if got := gitCommonDir(root); got != "" {
			t.Errorf("gitCommonDir() = %q, want empty", got)
		}
	})
}
