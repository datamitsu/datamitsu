package traverser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSortAscending(t *testing.T) {
	input := []string{"c.txt", "a.txt", "b.txt"}
	expected := []string{"a.txt", "b.txt", "c.txt"}

	result := SortAscending(input)

	for i, v := range expected {
		if result[i] != v {
			t.Errorf("result[%d] = %q, want %q", i, result[i], v)
		}
	}

	if input[0] != "c.txt" {
		t.Error("SortAscending() should not modify original slice")
	}
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name     string
		slice1   []string
		slice2   []string
		expected []string
	}{
		{
			name:     "basic diff",
			slice1:   []string{"a", "b", "c", "d"},
			slice2:   []string{"b", "d"},
			expected: []string{"a", "c"},
		},
		{
			name:     "no difference",
			slice1:   []string{"a", "b"},
			slice2:   []string{"a", "b"},
			expected: []string{},
		},
		{
			name:     "all different",
			slice1:   []string{"a", "b"},
			slice2:   []string{"c", "d"},
			expected: []string{"a", "b"},
		},
		{
			name:     "empty slice1",
			slice1:   []string{},
			slice2:   []string{"a", "b"},
			expected: []string{},
		},
		{
			name:     "empty slice2",
			slice1:   []string{"a", "b"},
			slice2:   []string{},
			expected: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Diff(tt.slice1, tt.slice2)

			if len(result) != len(tt.expected) {
				t.Errorf("len(result) = %d, want %d", len(result), len(tt.expected))
				return
			}

			for i, v := range tt.expected {
				if result[i] != v {
					t.Errorf("result[%d] = %q, want %q", i, result[i], v)
				}
			}
		})
	}
}

func TestFindFiles(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte(""), 0644)

	subdir := filepath.Join(tmpDir, "subdir")
	_ = os.MkdirAll(subdir, 0755)
	_ = os.WriteFile(filepath.Join(subdir, "file3.txt"), []byte(""), 0644)

	ctx := context.Background()
	files, err := FindFiles(ctx, tmpDir)
	if err != nil {
		t.Fatalf("FindFiles() error = %v", err)
	}

	if len(files) != 3 {
		t.Errorf("len(files) = %d, want 3", len(files))
	}

	sort.Strings(files)

	expectedFiles := []string{
		filepath.Join(tmpDir, "file1.txt"),
		filepath.Join(tmpDir, "file2.txt"),
		filepath.Join(tmpDir, "subdir", "file3.txt"),
	}
	sort.Strings(expectedFiles)

	for i, expected := range expectedFiles {
		if i >= len(files) {
			t.Errorf("missing file at index %d", i)
			continue
		}
		if files[i] != expected {
			t.Errorf("files[%d] = %q, want %q", i, files[i], expected)
		}
	}
}

func TestFindFilesWithGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	gitignore := filepath.Join(tmpDir, ".gitignore")
	_ = os.WriteFile(gitignore, []byte("*.log\nignored/\n"), 0644)

	_ = os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "file.log"), []byte(""), 0644)

	ignoredDir := filepath.Join(tmpDir, "ignored")
	_ = os.MkdirAll(ignoredDir, 0755)
	_ = os.WriteFile(filepath.Join(ignoredDir, "file.txt"), []byte(""), 0644)

	ctx := context.Background()
	files, err := FindFiles(ctx, tmpDir)
	if err != nil {
		t.Fatalf("FindFiles() error = %v", err)
	}

	for _, file := range files {
		if filepath.Ext(file) == ".log" {
			t.Errorf("found ignored .log file: %s", file)
		}
		if filepath.Base(filepath.Dir(file)) == "ignored" {
			t.Errorf("found file in ignored directory: %s", file)
		}
	}

	hasFileTxt := false
	for _, file := range files {
		if filepath.Base(file) == "file.txt" && filepath.Dir(file) == tmpDir {
			hasFileTxt = true
			break
		}
	}
	if !hasFileTxt {
		t.Error("file.txt should be included")
	}
}

func TestFindFilesSkipsGitDir(t *testing.T) {
	tmpDir := t.TempDir()

	gitDir := filepath.Join(tmpDir, ".git")
	_ = os.MkdirAll(gitDir, 0755)
	_ = os.WriteFile(filepath.Join(gitDir, "config"), []byte(""), 0644)

	_ = os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte(""), 0644)

	ctx := context.Background()
	files, err := FindFiles(ctx, tmpDir)
	if err != nil {
		t.Fatalf("FindFiles() error = %v", err)
	}

	for _, file := range files {
		if filepath.Base(filepath.Dir(file)) == ".git" {
			t.Errorf("found file in .git directory: %s", file)
		}
	}
}

func TestFindFilesNestedGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	rootGitignore := filepath.Join(tmpDir, ".gitignore")
	_ = os.WriteFile(rootGitignore, []byte("*.log\n"), 0644)

	subdir := filepath.Join(tmpDir, "subdir")
	_ = os.MkdirAll(subdir, 0755)
	subdirGitignore := filepath.Join(subdir, ".gitignore")
	_ = os.WriteFile(subdirGitignore, []byte("*.tmp\n"), 0644)

	_ = os.WriteFile(filepath.Join(tmpDir, "root.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "root.log"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(subdir, "sub.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(subdir, "sub.tmp"), []byte(""), 0644)

	ctx := context.Background()
	files, err := FindFiles(ctx, tmpDir)
	if err != nil {
		t.Fatalf("FindFiles() error = %v", err)
	}

	for _, file := range files {
		ext := filepath.Ext(file)
		if ext == ".log" {
			t.Errorf("found .log file (ignored by root): %s", file)
		}
		if ext == ".tmp" {
			t.Errorf("found .tmp file (ignored by subdir): %s", file)
		}
	}

	hasRootTxt := false
	hasSubTxt := false
	for _, file := range files {
		if file == filepath.Join(tmpDir, "root.txt") {
			hasRootTxt = true
		}
		if file == filepath.Join(subdir, "sub.txt") {
			hasSubTxt = true
		}
	}

	if !hasRootTxt {
		t.Error("root.txt should be included")
	}
	if !hasSubTxt {
		t.Error("sub.txt should be included")
	}
}

func TestWalk(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte(""), 0644)

	git := NewGitIgnore(tmpDir)
	_ = git.Compile()

	walker := Walker{
		rootPath: tmpDir,
		path:     tmpDir,
		git:      git,
	}

	files, err := walker.Walk(context.Background())
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	if len(files) != 2 {
		t.Errorf("len(files) = %d, want 2", len(files))
	}
}

func TestWalkEmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	git := NewGitIgnore(tmpDir)
	_ = git.Compile()

	walker := Walker{
		rootPath: tmpDir,
		path:     tmpDir,
		git:      git,
	}

	files, err := walker.Walk(context.Background())
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	if len(files) != 0 {
		t.Errorf("len(files) = %d, want 0", len(files))
	}
}

func TestWalkContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	for i := 0; i < 5; i++ {
		subdir := filepath.Join(tmpDir, fmt.Sprintf("dir%d", i))
		_ = os.MkdirAll(subdir, 0755)
		for j := 0; j < 10; j++ {
			_ = os.WriteFile(filepath.Join(subdir, fmt.Sprintf("file%d.txt", j)), []byte(""), 0644)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	git := NewGitIgnore(tmpDir)
	_ = git.Compile()

	walker := Walker{
		rootPath: tmpDir,
		path:     tmpDir,
		git:      git,
	}

	_, err := walker.Walk(ctx)
	if err == nil {
		t.Fatal("Walk() should return an error when context is cancelled")
	}
}

func TestWalkSkipsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "real.txt"), []byte("content"), 0644)

	targetFile := filepath.Join(tmpDir, "real.txt")
	symlinkFile := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(targetFile, symlinkFile); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	symlinkDir := filepath.Join(tmpDir, "linkdir")
	subdir := filepath.Join(tmpDir, "subdir")
	_ = os.MkdirAll(subdir, 0755)
	_ = os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("nested"), 0644)
	if err := os.Symlink(subdir, symlinkDir); err != nil {
		t.Skipf("directory symlinks not supported: %v", err)
	}

	ctx := context.Background()
	files, err := FindFiles(ctx, tmpDir)
	if err != nil {
		t.Fatalf("FindFiles() error = %v", err)
	}

	sort.Strings(files)

	for _, file := range files {
		if filepath.Base(file) == "link.txt" {
			t.Errorf("symlink file should be skipped: %s", file)
		}
	}

	hasReal := false
	hasNested := false
	for _, file := range files {
		if filepath.Base(file) == "real.txt" {
			hasReal = true
		}
		if filepath.Base(file) == "nested.txt" {
			hasNested = true
		}
	}
	if !hasReal {
		t.Error("real.txt should be included")
	}
	if !hasNested {
		t.Error("subdir/nested.txt should be included")
	}

	for _, file := range files {
		rel, _ := filepath.Rel(tmpDir, file)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for _, p := range parts {
			if p == "linkdir" {
				t.Errorf("files under symlinked directory should be skipped: %s", file)
			}
		}
	}
}

func TestWalkDeepNesting(t *testing.T) {
	tmpDir := t.TempDir()

	deep := tmpDir
	for i := 0; i < 10; i++ {
		deep = filepath.Join(deep, "level")
		_ = os.MkdirAll(deep, 0755)
		_ = os.WriteFile(filepath.Join(deep, "file.txt"), []byte(""), 0644)
	}

	ctx := context.Background()
	files, err := FindFiles(ctx, tmpDir)
	if err != nil {
		t.Fatalf("FindFiles() error = %v", err)
	}

	if len(files) != 10 {
		t.Errorf("len(files) = %d, want 10", len(files))
	}
}
