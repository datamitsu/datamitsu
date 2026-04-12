package traverser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewGitIgnore(t *testing.T) {
	root := "/tmp/test"
	gi := NewGitIgnore(root)

	if gi == nil {
		t.Fatal("NewGitIgnore() returned nil")
	}

	if gi.root != root {
		t.Errorf("root = %q, want %q", gi.root, root)
	}

	if gi.isCompiled {
		t.Error("isCompiled should be false initially")
	}
}

func TestAddGitIgnoreFile(t *testing.T) {
	gi := NewGitIgnore("/tmp/test")

	content := []byte("*.log\n")
	absPath := "/tmp/test/.gitignore"

	gi.AddGitIgnoreFile(absPath, content)

	if len(gi.list) != 1 {
		t.Errorf("len(list) = %d, want 1", len(gi.list))
	}

	if gi.list[0].absPath != absPath {
		t.Errorf("absPath = %q, want %q", gi.list[0].absPath, absPath)
	}

	if string(gi.list[0].content) != string(content) {
		t.Errorf("content = %q, want %q", string(gi.list[0].content), string(content))
	}
}

func TestAddGitIgnoreFilePanicsWhenCompiled(t *testing.T) {
	gi := NewGitIgnore("/tmp/test")
	gi.isCompiled = true

	defer func() {
		if r := recover(); r == nil {
			t.Error("AddGitIgnoreFile() should panic when already compiled")
		}
	}()

	gi.AddGitIgnoreFile("/tmp/.gitignore", []byte("test"))
}

func TestCompile(t *testing.T) {
	tmpDir := t.TempDir()
	gi := NewGitIgnore(tmpDir)

	content := []byte("*.log\nnode_modules/\n# comment\n")
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	gi.AddGitIgnoreFile(gitignorePath, content)

	err := gi.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if !gi.isCompiled {
		t.Error("isCompiled should be true after Compile()")
	}

	if len(gi.patterns) != 2 {
		t.Errorf("len(patterns) = %d, want 2", len(gi.patterns))
	}
}

func TestCountPatterns(t *testing.T) {
	tmpDir := t.TempDir()
	gi := NewGitIgnore(tmpDir)

	t.Run("not compiled", func(t *testing.T) {
		_, err := gi.CountPatterns()
		if err == nil {
			t.Error("CountPatterns() should return error when not compiled")
		}
	})

	t.Run("compiled", func(t *testing.T) {
		content := []byte("*.log\nnode_modules/\n")
		gitignorePath := filepath.Join(tmpDir, ".gitignore")
		gi.AddGitIgnoreFile(gitignorePath, content)
		_ = gi.Compile()

		count, err := gi.CountPatterns()
		if err != nil {
			t.Fatalf("CountPatterns() error = %v", err)
		}

		if count != 2 {
			t.Errorf("count = %d, want 2", count)
		}
	})
}

func TestIsIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	gi := NewGitIgnore(tmpDir)

	content := []byte("*.log\nnode_modules/\nbuild/\n")
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	gi.AddGitIgnoreFile(gitignorePath, content)
	_ = gi.Compile()

	tests := []struct {
		name     string
		path     string
		isDir    bool
		expected bool
	}{
		{
			name:     "ignored log file",
			path:     "test.log",
			isDir:    false,
			expected: true,
		},
		{
			name:     "not ignored txt file",
			path:     "test.txt",
			isDir:    false,
			expected: false,
		},
		{
			name:     "ignored node_modules dir",
			path:     "node_modules",
			isDir:    true,
			expected: true,
		},
		{
			name:     "ignored build dir",
			path:     "build",
			isDir:    true,
			expected: true,
		},
		{
			name:     "not ignored src dir",
			path:     "src",
			isDir:    true,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gi.IsIgnored(tt.path, tt.isDir)
			if result != tt.expected {
				t.Errorf("IsIgnored(%q, %v) = %v, want %v", tt.path, tt.isDir, result, tt.expected)
			}
		})
	}
}

func TestIsIgnoredPanicsWhenNotCompiled(t *testing.T) {
	gi := NewGitIgnore("/tmp/test")

	defer func() {
		if r := recover(); r == nil {
			t.Error("IsIgnored() should panic when not compiled")
		}
	}()

	gi.IsIgnored("test.txt", false)
}

func TestIsIgnoredWithAbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	gi := NewGitIgnore(tmpDir)

	content := []byte("*.log\n")
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	gi.AddGitIgnoreFile(gitignorePath, content)
	_ = gi.Compile()

	absPath := filepath.Join(tmpDir, "test.log")
	result := gi.IsIgnored(absPath, false)

	if !result {
		t.Error("IsIgnored() should work with absolute paths")
	}
}

func TestClone(t *testing.T) {
	tmpDir := t.TempDir()
	gi := NewGitIgnore(tmpDir)

	content := []byte("*.log\n")
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	gi.AddGitIgnoreFile(gitignorePath, content)

	cloned := gi.Clone()

	if cloned.root != gi.root {
		t.Errorf("cloned root = %q, want %q", cloned.root, gi.root)
	}

	if len(cloned.list) != len(gi.list) {
		t.Errorf("len(cloned.list) = %d, want %d", len(cloned.list), len(gi.list))
	}

	if cloned.isCompiled {
		t.Error("cloned should not be compiled")
	}
}

func TestCollectRules(t *testing.T) {
	tmpDir := t.TempDir()

	rootGitignore := filepath.Join(tmpDir, ".gitignore")
	_ = os.WriteFile(rootGitignore, []byte("*.log\n"), 0644)

	subdir := filepath.Join(tmpDir, "subdir")
	_ = os.MkdirAll(subdir, 0755)
	subdirGitignore := filepath.Join(subdir, ".gitignore")
	_ = os.WriteFile(subdirGitignore, []byte("*.tmp\n"), 0644)

	gi := NewGitIgnore(tmpDir)
	ctx := context.Background()

	err := gi.CollectRules(ctx, subdir)
	if err != nil {
		t.Fatalf("CollectRules() error = %v", err)
	}

	if len(gi.list) < 2 {
		t.Errorf("len(list) = %d, want at least 2", len(gi.list))
	}
}

func TestCollectRulesPanicsWhenCompiled(t *testing.T) {
	gi := NewGitIgnore("/tmp/test")
	gi.isCompiled = true
	ctx := context.Background()

	defer func() {
		if r := recover(); r == nil {
			t.Error("CollectRules() should panic when already compiled")
		}
	}()

	_ = gi.CollectRules(ctx, "/tmp/test")
}

func TestCollectRulesTargetOutsideRoot(t *testing.T) {
	tmpDir := t.TempDir()
	gi := NewGitIgnore(tmpDir)
	ctx := context.Background()

	err := gi.CollectRules(ctx, "/some/other/path")
	if err != nil {
		t.Fatalf("CollectRules() error = %v", err)
	}

	if len(gi.list) != 0 {
		t.Error("list should be empty when target is outside root")
	}
}

func TestIsIgnoredEmptyPatterns(t *testing.T) {
	tmpDir := t.TempDir()
	gi := NewGitIgnore(tmpDir)
	_ = gi.Compile()

	result := gi.IsIgnored("test.txt", false)
	if result {
		t.Error("IsIgnored() should return false when no patterns")
	}
}

func TestCompileWithSubdirectoryGitignore(t *testing.T) {
	tmpDir := t.TempDir()
	subdir := filepath.Join(tmpDir, "a", "b")
	_ = os.MkdirAll(subdir, 0755)

	gi := NewGitIgnore(tmpDir)

	subdirGitignore := filepath.Join(subdir, ".gitignore")
	content := []byte("*.test\n")
	gi.AddGitIgnoreFile(subdirGitignore, content)

	err := gi.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if len(gi.patterns) != 1 {
		t.Errorf("len(patterns) = %d, want 1", len(gi.patterns))
	}
}
