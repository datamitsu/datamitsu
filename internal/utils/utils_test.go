package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExists(t *testing.T) {
	t.Run("file exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")
		_ = os.WriteFile(filePath, []byte("test"), 0644)

		if !Exists(filePath) {
			t.Error("Exists() returned false for existing file")
		}
	})

	t.Run("directory exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		dirPath := filepath.Join(tmpDir, "testdir")
		_ = os.Mkdir(dirPath, 0755)

		if !Exists(dirPath) {
			t.Error("Exists() returned false for existing directory")
		}
	})

	t.Run("does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonExistent := filepath.Join(tmpDir, "nonexistent")

		if Exists(nonExistent) {
			t.Error("Exists() returned true for non-existent path")
		}
	})
}

func TestIsDir(t *testing.T) {
	t.Run("is directory", func(t *testing.T) {
		tmpDir := t.TempDir()

		if !IsDir(tmpDir) {
			t.Error("IsDir() returned false for directory")
		}
	})

	t.Run("is file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")
		_ = os.WriteFile(filePath, []byte("test"), 0644)

		if IsDir(filePath) {
			t.Error("IsDir() returned true for file")
		}
	})

	t.Run("does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonExistent := filepath.Join(tmpDir, "nonexistent")

		if IsDir(nonExistent) {
			t.Error("IsDir() returned true for non-existent path")
		}
	})
}

func TestIsFile(t *testing.T) {
	t.Run("is file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")
		_ = os.WriteFile(filePath, []byte("test"), 0644)

		if !IsFile(filePath) {
			t.Error("IsFile() returned false for file")
		}
	})

	t.Run("is directory", func(t *testing.T) {
		tmpDir := t.TempDir()

		if IsFile(tmpDir) {
			t.Error("IsFile() returned true for directory")
		}
	})

	t.Run("does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonExistent := filepath.Join(tmpDir, "nonexistent")

		if IsFile(nonExistent) {
			t.Error("IsFile() returned true for non-existent path")
		}
	})
}

func TestEnsureDir(t *testing.T) {
	t.Run("creates directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		newDir := filepath.Join(tmpDir, "newdir")

		err := EnsureDir(newDir)
		if err != nil {
			t.Fatalf("EnsureDir() error = %v", err)
		}

		if !IsDir(newDir) {
			t.Error("directory was not created")
		}
	})

	t.Run("creates nested directories", func(t *testing.T) {
		tmpDir := t.TempDir()
		nestedDir := filepath.Join(tmpDir, "a", "b", "c")

		err := EnsureDir(nestedDir)
		if err != nil {
			t.Fatalf("EnsureDir() error = %v", err)
		}

		if !IsDir(nestedDir) {
			t.Error("nested directories were not created")
		}
	})

	t.Run("directory already exists", func(t *testing.T) {
		tmpDir := t.TempDir()

		err := EnsureDir(tmpDir)
		if err != nil {
			t.Errorf("EnsureDir() error = %v", err)
		}
	})
}

func TestHomeDir(t *testing.T) {
	home, err := HomeDir()
	if err != nil {
		t.Fatalf("HomeDir() error = %v", err)
	}

	if home == "" {
		t.Error("HomeDir() returned empty string")
	}

	if !IsDir(home) {
		t.Error("HomeDir() returned path that is not a directory")
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := HomeDir()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tilde only",
			input: "~",
			want:  home,
		},
		{
			name:  "tilde with path",
			input: "~/Documents",
			want:  filepath.Join(home, "Documents"),
		},
		{
			name:  "absolute path",
			input: "/usr/local/bin",
			want:  "/usr/local/bin",
		},
		{
			name:  "relative path",
			input: "relative/path",
			want:  "relative/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandHome(tt.input)
			if got != tt.want {
				t.Errorf("ExpandHome(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReadFileIfExists(t *testing.T) {
	t.Run("file exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")
		testContent := []byte("test content")
		_ = os.WriteFile(filePath, testContent, 0644)

		content, err := ReadFileIfExists(filePath)
		if err != nil {
			t.Fatalf("ReadFileIfExists() error = %v", err)
		}

		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonExistent := filepath.Join(tmpDir, "nonexistent")

		content, err := ReadFileIfExists(nonExistent)
		if err != nil {
			t.Fatalf("ReadFileIfExists() error = %v", err)
		}

		if content != nil {
			t.Error("expected nil content for non-existent file")
		}
	})
}

func TestRenameReplace(t *testing.T) {
	t.Run("rename to new file", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "src")
		dst := filepath.Join(tmpDir, "dst")
		_ = os.WriteFile(src, []byte("content"), 0644)

		err := RenameReplace(src, dst)
		if err != nil {
			t.Fatalf("RenameReplace() error = %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("failed to read dst: %v", err)
		}
		if string(data) != "content" {
			t.Errorf("got %q, want %q", string(data), "content")
		}
		if Exists(src) {
			t.Error("src should no longer exist")
		}
	})

	t.Run("replace existing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "src")
		dst := filepath.Join(tmpDir, "dst")
		_ = os.WriteFile(dst, []byte("old"), 0644)
		_ = os.WriteFile(src, []byte("new"), 0644)

		err := RenameReplace(src, dst)
		if err != nil {
			t.Fatalf("RenameReplace() error = %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("failed to read dst: %v", err)
		}
		if string(data) != "new" {
			t.Errorf("got %q, want %q", string(data), "new")
		}
	})
}

func TestWriteFile(t *testing.T) {
	t.Run("writes file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")
		testContent := []byte("test content")

		err := WriteFile(filePath, testContent)
		if err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "a", "b", "c", "testfile")
		testContent := []byte("test")

		err := WriteFile(filePath, testContent)
		if err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		if !Exists(filePath) {
			t.Error("file was not created")
		}

		if !IsDir(filepath.Dir(filePath)) {
			t.Error("parent directories were not created")
		}
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")

		_ = os.WriteFile(filePath, []byte("old content"), 0644)

		newContent := []byte("new content")
		err := WriteFile(filePath, newContent)
		if err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		content, _ := os.ReadFile(filePath)
		if string(content) != string(newContent) {
			t.Error("file was not overwritten")
		}
	})
}
