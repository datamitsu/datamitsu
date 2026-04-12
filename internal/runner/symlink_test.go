package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterSymlinkPaths(t *testing.T) {
	t.Run("filters out symlinks", func(t *testing.T) {
		dir := t.TempDir()

		regular1 := filepath.Join(dir, "file1.go")
		regular2 := filepath.Join(dir, "file2.go")
		symlink1 := filepath.Join(dir, "link1.go")

		if err := os.WriteFile(regular1, []byte("package main"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(regular2, []byte("package main"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(regular1, symlink1); err != nil {
			t.Fatal(err)
		}

		result := filterSymlinkPaths([]string{regular1, symlink1, regular2})

		if len(result) != 2 {
			t.Fatalf("expected 2 files, got %d: %v", len(result), result)
		}
		if result[0] != regular1 {
			t.Errorf("result[0] = %q, want %q", result[0], regular1)
		}
		if result[1] != regular2 {
			t.Errorf("result[1] = %q, want %q", result[1], regular2)
		}
	})

	t.Run("non-existent paths kept as-is", func(t *testing.T) {
		paths := []string{"/nonexistent/path/file.go", "/another/missing.go"}
		result := filterSymlinkPaths(paths)

		if len(result) != 2 {
			t.Fatalf("expected 2 files, got %d: %v", len(result), result)
		}
		if result[0] != paths[0] {
			t.Errorf("result[0] = %q, want %q", result[0], paths[0])
		}
		if result[1] != paths[1] {
			t.Errorf("result[1] = %q, want %q", result[1], paths[1])
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		result := filterSymlinkPaths([]string{})
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("nil slice", func(t *testing.T) {
		result := filterSymlinkPaths(nil)
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("all symlinks", func(t *testing.T) {
		dir := t.TempDir()

		target := filepath.Join(dir, "target.go")
		if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
			t.Fatal(err)
		}

		link1 := filepath.Join(dir, "link1.go")
		link2 := filepath.Join(dir, "link2.go")
		if err := os.Symlink(target, link1); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link2); err != nil {
			t.Fatal(err)
		}

		result := filterSymlinkPaths([]string{link1, link2})
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("no symlinks", func(t *testing.T) {
		dir := t.TempDir()

		file1 := filepath.Join(dir, "file1.go")
		file2 := filepath.Join(dir, "file2.go")
		if err := os.WriteFile(file1, []byte("a"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file2, []byte("b"), 0644); err != nil {
			t.Fatal(err)
		}

		result := filterSymlinkPaths([]string{file1, file2})
		if len(result) != 2 {
			t.Fatalf("expected 2 files, got %d", len(result))
		}
	})
}
