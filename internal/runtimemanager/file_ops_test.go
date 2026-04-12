package runtimemanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveFile(t *testing.T) {
	t.Run("rename within same directory", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.txt")
		dst := filepath.Join(dir, "dst.txt")

		if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := moveFile(src, dst); err != nil {
			t.Fatalf("moveFile error = %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("failed to read dst: %v", err)
		}
		if string(data) != "hello" {
			t.Errorf("dst content = %q, want %q", string(data), "hello")
		}

		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Error("src should not exist after move")
		}
	})

	t.Run("move preserves file permissions", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "exec")
		dst := filepath.Join(dir, "exec-dst")

		if err := os.WriteFile(src, []byte("#!/bin/sh"), 0755); err != nil {
			t.Fatal(err)
		}

		if err := moveFile(src, dst); err != nil {
			t.Fatalf("moveFile error = %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat error = %v", err)
		}

		if info.Mode().Perm()&0100 == 0 {
			t.Error("dst should be executable")
		}
	})

	t.Run("cross-directory move", func(t *testing.T) {
		dir1 := t.TempDir()
		dir2 := t.TempDir()
		src := filepath.Join(dir1, "file.bin")
		dst := filepath.Join(dir2, "file.bin")

		if err := os.WriteFile(src, []byte("binary data"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := moveFile(src, dst); err != nil {
			t.Fatalf("moveFile error = %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read error = %v", err)
		}
		if string(data) != "binary data" {
			t.Errorf("content = %q, want %q", string(data), "binary data")
		}
	})

	t.Run("source not found", func(t *testing.T) {
		dir := t.TempDir()
		err := moveFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst"))
		if err == nil {
			t.Error("expected error for nonexistent source")
		}
	})
}

func TestMoveRuntimeFiles(t *testing.T) {
	t.Run("file source without binaryPath", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "runtime-binary")
		dst := filepath.Join(dir, "cache", "pnpm", "abc123")

		if err := os.WriteFile(src, []byte("pnpm-binary"), 0755); err != nil {
			t.Fatal(err)
		}

		if err := moveRuntimeFiles(src, dst, nil); err != nil {
			t.Fatalf("moveRuntimeFiles error = %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read error = %v", err)
		}
		if string(data) != "pnpm-binary" {
			t.Errorf("content = %q, want %q", string(data), "pnpm-binary")
		}
	})

	t.Run("file source with binaryPath", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "extracted-binary")
		dstBase := filepath.Join(dir, "cache", "uv", "abc123")
		bp := "uv-x86_64-unknown-linux-gnu/uv"

		if err := os.WriteFile(src, []byte("uv-binary"), 0755); err != nil {
			t.Fatal(err)
		}

		if err := moveRuntimeFiles(src, dstBase, &bp); err != nil {
			t.Fatalf("moveRuntimeFiles error = %v", err)
		}

		expectedPath := filepath.Join(dstBase, "uv-x86_64-unknown-linux-gnu", "uv")
		data, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("read error = %v", err)
		}
		if string(data) != "uv-binary" {
			t.Errorf("content = %q, want %q", string(data), "uv-binary")
		}
	})

	t.Run("directory source", func(t *testing.T) {
		dir := t.TempDir()
		srcDir := filepath.Join(dir, "extracted")
		if err := os.MkdirAll(srcDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "bin1"), []byte("binary1"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "bin2"), []byte("binary2"), 0755); err != nil {
			t.Fatal(err)
		}

		dstDir := filepath.Join(dir, "cache", "runtime", "hash")
		if err := moveRuntimeFiles(srcDir, dstDir, nil); err != nil {
			t.Fatalf("moveRuntimeFiles error = %v", err)
		}

		data1, err := os.ReadFile(filepath.Join(dstDir, "bin1"))
		if err != nil {
			t.Fatalf("read bin1 error = %v", err)
		}
		if string(data1) != "binary1" {
			t.Errorf("bin1 = %q, want %q", string(data1), "binary1")
		}

		data2, err := os.ReadFile(filepath.Join(dstDir, "bin2"))
		if err != nil {
			t.Fatalf("read bin2 error = %v", err)
		}
		if string(data2) != "binary2" {
			t.Errorf("bin2 = %q, want %q", string(data2), "binary2")
		}
	})
}
