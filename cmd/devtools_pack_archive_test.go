package cmd

import (
	"archive/tar"
	"bytes"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/constants"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPackInlineArchiveSingleFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	output := runPackCommand(t, dir)

	if !strings.HasPrefix(output, binmanager.TarBrotliPrefix) {
		t.Fatalf("expected tar.br: prefix, got: %s", output[:20])
	}

	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatalf("failed to decompress output: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("decompressed data is empty")
	}
}

func TestPackInlineArchiveNestedDirs(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "file.txt"), []byte("nested content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root content"), 0o644); err != nil {
		t.Fatal(err)
	}

	output := runPackCommand(t, dir)

	if !strings.HasPrefix(output, binmanager.TarBrotliPrefix) {
		t.Fatalf("expected tar.br: prefix, got: %s", output[:20])
	}

	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatalf("failed to decompress output: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("decompressed data is empty")
	}
}

func TestPackInlineArchiveMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	output := runPackCommand(t, dir)

	if !strings.HasPrefix(output, binmanager.TarBrotliPrefix) {
		t.Fatalf("expected tar.br: prefix")
	}

	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("decompressed data is empty")
	}
}

func TestPackInlineArchiveEmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	output := runPackCommand(t, dir)

	if !strings.HasPrefix(output, binmanager.TarBrotliPrefix) {
		t.Fatalf("expected tar.br: prefix")
	}

	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty tar (tar has end-of-archive marker)")
	}
}

func TestPackInlineArchiveNotADirectory(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := packInlineArchiveCmd
	cmd.SetArgs([]string{filePath})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.RunE(cmd, []string{filePath})
	if err == nil {
		t.Fatal("expected error for non-directory input")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected 'not a directory' error, got: %v", err)
	}
}

func TestPackInlineArchiveNonExistentPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent")

	cmd := packInlineArchiveCmd
	cmd.SetArgs([]string{path})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.RunE(cmd, []string{path})
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestPackInlineArchiveWarnThreshold(t *testing.T) {
	dir := t.TempDir()
	// Create a file slightly over the warn threshold
	data := make([]byte, constants.WarnInlineArchiveSize+1)
	if err := os.WriteFile(filepath.Join(dir, "large.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cmd := packInlineArchiveCmd
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.RunE(cmd, []string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that warning was written to stderr
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "warning") {
		t.Errorf("expected warning on stderr, got: %s", stderrStr)
	}
}

func TestPackInlineArchiveHardLimit(t *testing.T) {
	dir := t.TempDir()
	// Create a file over the hard limit
	data := make([]byte, constants.MaxInlineArchiveSize+1)
	if err := os.WriteFile(filepath.Join(dir, "huge.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cmd := packInlineArchiveCmd
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.RunE(cmd, []string{dir})
	if err == nil {
		t.Fatal("expected error for oversized archive")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("expected 'exceeds maximum' error, got: %v", err)
	}
}

func TestLimitedWriterEnforcesLimit(t *testing.T) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, limit: 100}

	// Write within limit should succeed
	n, err := lw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes written, got %d", n)
	}

	// Write that would exceed limit should fail without writing
	bigData := make([]byte, 100)
	_, err = lw.Write(bigData)
	if err == nil {
		t.Fatal("expected error when exceeding limit")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("expected 'exceeds maximum' error, got: %v", err)
	}
	// Buffer should not have grown beyond the first write
	if buf.Len() != 5 {
		t.Fatalf("expected buffer to remain at 5 bytes, got %d", buf.Len())
	}
}

func TestCreateTarFromDirSingleFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := createTarFromDir(dir, constants.MaxInlineArchiveSize)
	if err != nil {
		t.Fatalf("createTarFromDir failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("tar data is empty")
	}
}

func TestCreateTarFromDirEmpty(t *testing.T) {
	dir := t.TempDir()

	data, err := createTarFromDir(dir, constants.MaxInlineArchiveSize)
	if err != nil {
		t.Fatalf("createTarFromDir failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("tar data should have end-of-archive marker")
	}
}

func TestPackInlineArchiveCommandRegistered(t *testing.T) {
	var found bool
	for _, cmd := range devtoolsCmd.Commands() {
		if cmd.Use == "pack-inline-archive <directory>" {
			found = true
			break
		}
	}
	if !found {
		t.Error("devtools command missing 'pack-inline-archive' subcommand")
	}
}

func runPackCommand(t *testing.T, dir string) string {
	t.Helper()
	var stdout bytes.Buffer
	cmd := packInlineArchiveCmd
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.RunE(cmd, []string{dir})
	if err != nil {
		t.Fatalf("runPackInlineArchive failed: %v", err)
	}
	return stdout.String()
}

func TestPackInlineArchiveDeterminism(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"a.txt":       "content a",
		"sub/b.txt":   "content b",
		"z.txt":       "content z",
	}

	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		pastTime := time.Now().Add(-24 * time.Hour)
		if err := os.Chtimes(fullPath, pastTime, pastTime); err != nil {
			t.Fatal(err)
		}
	}

	output1 := runPackCommand(t, dir)
	output2 := runPackCommand(t, dir)

	if output1 != output2 {
		t.Error("archives are not deterministic: same directory produced different outputs")
	}
}

func TestPackInlineArchiveOrdering(t *testing.T) {
	dir := t.TempDir()

	files := []string{"z.txt", "a.txt", "m.txt", "b.txt"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	output := runPackCommand(t, dir)
	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatal(err)
	}

	tarReader := tar.NewReader(bytes.NewReader(data))
	var seenNames []string
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seenNames = append(seenNames, header.Name)
	}

	expected := []string{"a.txt", "b.txt", "m.txt", "z.txt"}
	if !slices.Equal(seenNames, expected) {
		t.Errorf("files not in sorted order: got %v, want %v", seenNames, expected)
	}
}

func TestPackInlineArchiveNormalizedHeaders(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	output := runPackCommand(t, dir)
	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatal(err)
	}

	tarReader := tar.NewReader(bytes.NewReader(data))
	header, err := tarReader.Next()
	if err != nil {
		t.Fatal(err)
	}

	epoch := time.Unix(0, 0).UTC()
	if !header.ModTime.Equal(epoch) {
		t.Errorf("ModTime not normalized: got %v, want %v", header.ModTime, epoch)
	}
	if header.Uid != 0 {
		t.Errorf("Uid not normalized: got %d, want 0", header.Uid)
	}
	if header.Gid != 0 {
		t.Errorf("Gid not normalized: got %d, want 0", header.Gid)
	}
	if header.Uname != "" {
		t.Errorf("Uname not normalized: got %q, want empty", header.Uname)
	}
	if header.Gname != "" {
		t.Errorf("Gname not normalized: got %q, want empty", header.Gname)
	}

	if header.Mode != 0o644 {
		t.Errorf("Mode not preserved: got %o, want 0644", header.Mode)
	}
}

func TestPackInlineArchiveCrossInvocationDeterminism(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	createTestFiles := func(base string) {
		files := map[string]string{
			"file1.txt":     "content 1",
			"sub/file2.txt": "content 2",
		}
		for path, content := range files {
			fullPath := filepath.Join(base, path)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	createTestFiles(dir1)
	createTestFiles(dir2)

	output1 := runPackCommand(t, dir1)
	output2 := runPackCommand(t, dir2)

	if output1 != output2 {
		t.Error("identical directories in different locations produced different archives")
	}
}

func TestPackInlineArchivePreservesPermissions(t *testing.T) {
	dir := t.TempDir()

	regularFile := filepath.Join(dir, "regular.txt")
	executableFile := filepath.Join(dir, "executable.sh")

	if err := os.WriteFile(regularFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executableFile, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	output := runPackCommand(t, dir)
	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatal(err)
	}

	tarReader := tar.NewReader(bytes.NewReader(data))
	perms := make(map[string]int64)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		perms[header.Name] = header.Mode
	}

	if perms["executable.sh"] != 0o755 {
		t.Errorf("executable permission not preserved: got %o, want 0755", perms["executable.sh"])
	}
	if perms["regular.txt"] != 0o644 {
		t.Errorf("regular file permission not preserved: got %o, want 0644", perms["regular.txt"])
	}
}

func TestPackInlineArchiveEmptySubdirectories(t *testing.T) {
	dir := t.TempDir()

	emptyDir := filepath.Join(dir, "empty", "nested")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	output := runPackCommand(t, dir)
	data, err := binmanager.DecompressArchive(output)
	if err != nil {
		t.Fatal(err)
	}

	tarReader := tar.NewReader(bytes.NewReader(data))
	var entries []string
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, header.Name)
	}

	expected := []string{"empty/", "empty/nested/", "file.txt"}
	if !slices.Equal(entries, expected) {
		t.Errorf("unexpected entries: got %v, want %v", entries, expected)
	}
}
