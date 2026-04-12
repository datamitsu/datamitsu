package binmanager

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func TestExtractBinaryFile(t *testing.T) {
	t.Run("successful copy", func(t *testing.T) {
		tmpDir := t.TempDir()

		srcPath := filepath.Join(tmpDir, "source.bin")
		testContent := []byte("binary content")
		if err := os.WriteFile(srcPath, testContent, 0644); err != nil {
			t.Fatalf("failed to create source file: %v", err)
		}

		dstPath, err := extractBinaryFile(srcPath, tmpDir)
		if err != nil {
			t.Fatalf("extractBinaryFile() error = %v", err)
		}

		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			t.Error("extracted file does not exist")
		}

		content, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("source file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcPath := filepath.Join(tmpDir, "nonexistent.bin")

		_, err := extractBinaryFile(srcPath, tmpDir)
		if err == nil {
			t.Error("expected error for nonexistent source file, got nil")
		}
	})
}

func TestExtractGz(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()

		testContent := []byte("gzipped content")
		gzPath := filepath.Join(tmpDir, "test.gz")

		gzFile, err := os.Create(gzPath)
		if err != nil {
			t.Fatalf("failed to create gz file: %v", err)
		}
		gzWriter := gzip.NewWriter(gzFile)
		_, _ = gzWriter.Write(testContent)
		_ = gzWriter.Close()
		_ = gzFile.Close()

		extractedPath, err := extractGz(gzPath, nil, tmpDir)
		if err != nil {
			t.Fatalf("extractGz() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		gzPath := filepath.Join(tmpDir, "nonexistent.gz")

		_, err := extractGz(gzPath, nil, tmpDir)
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("invalid gz file", func(t *testing.T) {
		tmpDir := t.TempDir()
		gzPath := filepath.Join(tmpDir, "invalid.gz")

		if err := os.WriteFile(gzPath, []byte("not a gz file"), 0644); err != nil {
			t.Fatalf("failed to create invalid gz file: %v", err)
		}

		_, err := extractGz(gzPath, nil, tmpDir)
		if err == nil {
			t.Error("expected error for invalid gz file, got nil")
		}
	})
}

func TestExtractTarGz(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()

		testContent := []byte("file content in tar.gz")
		targetPath := "bin/myapp"
		tarGzPath := filepath.Join(tmpDir, "test.tar.gz")

		file, _ := os.Create(tarGzPath)
		gzWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzWriter)

		header := &tar.Header{
			Name: targetPath,
			Mode: 0755,
			Size: int64(len(testContent)),
		}
		_ = tarWriter.WriteHeader(header)
		_, _ = tarWriter.Write(testContent)
		_ = tarWriter.Close()
		_ = gzWriter.Close()
		_ = file.Close()

		extractedPath, err := extractTarGz(tarGzPath, &targetPath, tmpDir)
		if err != nil {
			t.Fatalf("extractTarGz() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("binaryPath is nil", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarGzPath := filepath.Join(tmpDir, "test.tar.gz")

		_, err := extractTarGz(tarGzPath, nil, tmpDir)
		if err == nil {
			t.Error("expected error for nil binaryPath, got nil")
		}
	})

	t.Run("file not found in archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarGzPath := filepath.Join(tmpDir, "test.tar.gz")

		file, _ := os.Create(tarGzPath)
		gzWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzWriter)

		testContent := []byte("content")
		header := &tar.Header{
			Name: "other/file",
			Mode: 0755,
			Size: int64(len(testContent)),
		}
		_ = tarWriter.WriteHeader(header)
		_, _ = tarWriter.Write(testContent)
		_ = tarWriter.Close()
		_ = gzWriter.Close()
		_ = file.Close()

		targetPath := "bin/myapp"
		_, err := extractTarGz(tarGzPath, &targetPath, tmpDir)
		if err == nil {
			t.Error("expected error for file not found, got nil")
		}
	})
}

func TestExtractTarXz(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()

		testContent := []byte("file content in tar.xz")
		targetPath := "bin/myapp"
		tarXzPath := filepath.Join(tmpDir, "test.tar.xz")

		file, _ := os.Create(tarXzPath)
		xzWriter, _ := xz.NewWriter(file)
		tarWriter := tar.NewWriter(xzWriter)

		header := &tar.Header{
			Name: targetPath,
			Mode: 0755,
			Size: int64(len(testContent)),
		}
		_ = tarWriter.WriteHeader(header)
		_, _ = tarWriter.Write(testContent)
		_ = tarWriter.Close()
		_ = xzWriter.Close()
		_ = file.Close()

		extractedPath, err := extractTarXz(tarXzPath, &targetPath, tmpDir)
		if err != nil {
			t.Fatalf("extractTarXz() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("binaryPath is nil", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarXzPath := filepath.Join(tmpDir, "test.tar.xz")

		_, err := extractTarXz(tarXzPath, nil, tmpDir)
		if err == nil {
			t.Error("expected error for nil binaryPath, got nil")
		}
	})
}

func TestExtractZip(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()

		testContent := []byte("file content in zip")
		targetPath := "bin/myapp"
		zipPath := filepath.Join(tmpDir, "test.zip")

		file, _ := os.Create(zipPath)
		zipWriter := zip.NewWriter(file)

		fileWriter, _ := zipWriter.Create(targetPath)
		_, _ = fileWriter.Write(testContent)
		_ = zipWriter.Close()
		_ = file.Close()

		extractedPath, err := extractZip(zipPath, &targetPath, tmpDir)
		if err != nil {
			t.Fatalf("extractZip() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("binaryPath is nil", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "test.zip")

		_, err := extractZip(zipPath, nil, tmpDir)
		if err == nil {
			t.Error("expected error for nil binaryPath, got nil")
		}
	})

	t.Run("file not found in archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "test.zip")

		file, _ := os.Create(zipPath)
		zipWriter := zip.NewWriter(file)

		fileWriter, _ := zipWriter.Create("other/file")
		_, _ = fileWriter.Write([]byte("content"))
		_ = zipWriter.Close()
		_ = file.Close()

		targetPath := "bin/myapp"
		_, err := extractZip(zipPath, &targetPath, tmpDir)
		if err == nil {
			t.Error("expected error for file not found, got nil")
		}
	})
}

func TestExtractBinary(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		contentType BinContentType
		wantErr     bool
	}{
		{
			name:        "binary type",
			contentType: BinContentTypeBinary,
			wantErr:     false,
		},
		{
			name:        "unsupported type",
			contentType: BinContentType("unknown"),
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archivePath := filepath.Join(tmpDir, "test.bin")
			if tt.contentType == BinContentTypeBinary {
				_ = os.WriteFile(archivePath, []byte("test"), 0644)
			}

			_, err := extractBinary(archivePath, tt.contentType, nil, tmpDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractBinary() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		name        string
		archivePath string
		targetPath  string
		want        bool
	}{
		{
			name:        "exact match",
			archivePath: "bin/myapp",
			targetPath:  "bin/myapp",
			want:        true,
		},
		{
			name:        "suffix match",
			archivePath: "prefix/bin/myapp",
			targetPath:  "bin/myapp",
			want:        true,
		},
		{
			name:        "basename match",
			archivePath: "some/path/myapp",
			targetPath:  "other/path/myapp",
			want:        true,
		},
		{
			name:        "no match",
			archivePath: "bin/otherapp",
			targetPath:  "bin/myapp",
			want:        false,
		},
		{
			name:        "basename match: subdir/tool matches tool",
			archivePath: "evil/tool",
			targetPath:  "tool",
			want:        true,
		},
		{
			name:        "basename match: bin/tool matches tool",
			archivePath: "bin/tool",
			targetPath:  "tool",
			want:        true,
		},
		{
			name:        "reject traversal in basename match: bin/../evil/tool",
			archivePath: "bin/../evil/tool",
			targetPath:  "tool",
			want:        false,
		},
		{
			name:        "reject traversal suffix match: bin/../evil/tool via suffix",
			archivePath: "bin/../evil/tool",
			targetPath:  "evil/tool",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchPath(tt.archivePath, tt.targetPath)
			if got != tt.want {
				t.Errorf("matchPath(%q, %q) = %v, want %v", tt.archivePath, tt.targetPath, got, tt.want)
			}
		})
	}
}

func TestValidateArchivePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"safe relative path", "bin/myapp", false},
		{"safe nested path", "foo/bar/baz", false},
		{"absolute unix path", "/etc/passwd", true},
		{"parent traversal prefix", "../etc/passwd", true},
		{"parent traversal nested", "foo/../../bar", true},
		{"parent traversal hidden", "foo/../../../etc/passwd", true},
		{"current dir reference", "./myapp", false},
		{"trailing slash", "foo/bar/", false},
		{"empty path", "", true},
		{"dot only", ".", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateArchivePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateArchivePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestMatchPath_SecurityValidation(t *testing.T) {
	tests := []struct {
		name        string
		archivePath string
		targetPath  string
		wantMatch   bool
		reason      string
	}{
		{
			name:        "reject traversal in archive path",
			archivePath: "../../../bin/myapp",
			targetPath:  "bin/myapp",
			wantMatch:   false,
			reason:      "path traversal should be rejected",
		},
		{
			name:        "reject absolute in archive path",
			archivePath: "/usr/bin/myapp",
			targetPath:  "myapp",
			wantMatch:   false,
			reason:      "absolute paths should be rejected",
		},
		{
			name:        "accept safe match",
			archivePath: "release/bin/myapp",
			targetPath:  "bin/myapp",
			wantMatch:   true,
			reason:      "safe paths should match normally",
		},
		{
			name:        "accept exact match",
			archivePath: "bin/myapp",
			targetPath:  "bin/myapp",
			wantMatch:   true,
			reason:      "exact safe matches should work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchPath(tt.archivePath, tt.targetPath)
			if got != tt.wantMatch {
				t.Errorf("matchPath(%q, %q) = %v, want %v: %s",
					tt.archivePath, tt.targetPath, got, tt.wantMatch, tt.reason)
			}
		})
	}
}

func TestExtractTarGz_PathTraversalProtection(t *testing.T) {
	tmpDir := t.TempDir()

	tarGzPath := filepath.Join(tmpDir, "malicious.tar.gz")
	file, _ := os.Create(tarGzPath)
	gzWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzWriter)

	maliciousContent := []byte("malicious content")
	header := &tar.Header{
		Name: "../../../etc/passwd",
		Mode: 0755,
		Size: int64(len(maliciousContent)),
	}
	_ = tarWriter.WriteHeader(header)
	_, _ = tarWriter.Write(maliciousContent)
	_ = tarWriter.Close()
	_ = gzWriter.Close()
	_ = file.Close()

	targetPath := "passwd"
	_, err := extractTarGz(tarGzPath, &targetPath, tmpDir)

	if err == nil {
		t.Error("expected error when extracting archive with path traversal, got nil")
	}
}

func TestExtractTarGzToDir(t *testing.T) {
	t.Run("extracts directories files and symlinks", func(t *testing.T) {
		tmpDir := t.TempDir()

		tarGzPath := filepath.Join(tmpDir, "test.tar.gz")
		file, err := os.Create(tarGzPath)
		if err != nil {
			t.Fatalf("failed to create tar.gz file: %v", err)
		}
		gzWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzWriter)

		_ = tarWriter.WriteHeader(&tar.Header{Name: "jdk/", Typeflag: tar.TypeDir, Mode: 0755})
		_ = tarWriter.WriteHeader(&tar.Header{Name: "jdk/bin/", Typeflag: tar.TypeDir, Mode: 0755})

		javaContent := []byte("#!/bin/sh\necho java")
		_ = tarWriter.WriteHeader(&tar.Header{Name: "jdk/bin/java", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(javaContent))})
		_, _ = tarWriter.Write(javaContent)

		libContent := []byte("library data")
		_ = tarWriter.WriteHeader(&tar.Header{Name: "jdk/lib/libjvm.so", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(libContent))})
		_, _ = tarWriter.Write(libContent)

		_ = tarWriter.WriteHeader(&tar.Header{Name: "jdk/bin/javac", Typeflag: tar.TypeSymlink, Linkname: "java"})

		_ = tarWriter.Close()
		_ = gzWriter.Close()
		_ = file.Close()

		destDir := t.TempDir()
		extractedDir, err := extractTarGzToDir(tarGzPath, destDir)
		if err != nil {
			t.Fatalf("extractTarGzToDir() error = %v", err)
		}

		javaPath := filepath.Join(extractedDir, "jdk/bin/java")
		content, err := os.ReadFile(javaPath)
		if err != nil {
			t.Fatalf("failed to read java binary: %v", err)
		}
		if string(content) != string(javaContent) {
			t.Errorf("java content mismatch: got %q, want %q", string(content), string(javaContent))
		}

		info, err := os.Stat(javaPath)
		if err != nil {
			t.Fatalf("failed to stat java: %v", err)
		}
		if info.Mode()&0100 == 0 {
			t.Error("java binary should be executable")
		}

		libPath := filepath.Join(extractedDir, "jdk/lib/libjvm.so")
		libData, err := os.ReadFile(libPath)
		if err != nil {
			t.Fatalf("failed to read lib file: %v", err)
		}
		if string(libData) != string(libContent) {
			t.Errorf("lib content mismatch: got %q, want %q", string(libData), string(libContent))
		}

		symlinkPath := filepath.Join(extractedDir, "jdk/bin/javac")
		target, err := os.Readlink(symlinkPath)
		if err != nil {
			t.Fatalf("failed to readlink javac: %v", err)
		}
		if target != "java" {
			t.Errorf("symlink target = %q, want %q", target, "java")
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		destDir := t.TempDir()
		_, err := extractTarGzToDir(filepath.Join(destDir, "nonexistent.tar.gz"), destDir)
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("rejects absolute symlinks", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarGzPath := filepath.Join(tmpDir, "abs-symlink.tar.gz")
		file, _ := os.Create(tarGzPath)
		gzWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzWriter)

		_ = tarWriter.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
		_ = tarWriter.Close()
		_ = gzWriter.Close()
		_ = file.Close()

		destDir := t.TempDir()
		extractedDir, err := extractTarGzToDir(tarGzPath, destDir)
		if err != nil {
			t.Fatalf("extractTarGzToDir() error = %v", err)
		}

		linkPath := filepath.Join(extractedDir, "link")
		if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
			t.Error("absolute symlink should have been skipped")
		}
	})

	t.Run("rejects path traversal in entries", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarGzPath := filepath.Join(tmpDir, "traversal.tar.gz")
		file, _ := os.Create(tarGzPath)
		gzWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzWriter)

		content := []byte("malicious")
		_ = tarWriter.WriteHeader(&tar.Header{Name: "../../../etc/passwd", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(content))})
		_, _ = tarWriter.Write(content)
		_ = tarWriter.Close()
		_ = gzWriter.Close()
		_ = file.Close()

		destDir := t.TempDir()
		extractedDir, err := extractTarGzToDir(tarGzPath, destDir)
		if err != nil {
			t.Fatalf("extractTarGzToDir() error = %v", err)
		}

		// The traversal entry should have been skipped, so the extracted dir should be empty or not contain the malicious file
		passwdPath := filepath.Join(extractedDir, "../../../etc/passwd")
		if _, err := os.Stat(passwdPath); err == nil {
			t.Error("path traversal entry should have been skipped")
		}
	})
}

func TestExtractTarXzToDir(t *testing.T) {
	t.Run("extracts directories and files", func(t *testing.T) {
		tmpDir := t.TempDir()

		tarXzPath := filepath.Join(tmpDir, "test.tar.xz")
		file, err := os.Create(tarXzPath)
		if err != nil {
			t.Fatalf("failed to create tar.xz file: %v", err)
		}
		xzWriter, err := xz.NewWriter(file)
		if err != nil {
			t.Fatalf("failed to create xz writer: %v", err)
		}
		tarWriter := tar.NewWriter(xzWriter)

		_ = tarWriter.WriteHeader(&tar.Header{Name: "app/", Typeflag: tar.TypeDir, Mode: 0755})

		binContent := []byte("binary content")
		_ = tarWriter.WriteHeader(&tar.Header{Name: "app/bin", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(binContent))})
		_, _ = tarWriter.Write(binContent)

		_ = tarWriter.Close()
		_ = xzWriter.Close()
		_ = file.Close()

		destDir := t.TempDir()
		extractedDir, err := extractTarXzToDir(tarXzPath, destDir)
		if err != nil {
			t.Fatalf("extractTarXzToDir() error = %v", err)
		}

		binPath := filepath.Join(extractedDir, "app/bin")
		content, err := os.ReadFile(binPath)
		if err != nil {
			t.Fatalf("failed to read extracted binary: %v", err)
		}
		if string(content) != string(binContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(binContent))
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		destDir := t.TempDir()
		_, err := extractTarXzToDir(filepath.Join(destDir, "nonexistent.tar.xz"), destDir)
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})
}

func TestExtractBinaryToDir(t *testing.T) {
	t.Run("tar.gz dispatches correctly", func(t *testing.T) {
		tmpDir := t.TempDir()

		tarGzPath := filepath.Join(tmpDir, "test.tar.gz")
		file, _ := os.Create(tarGzPath)
		gzWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzWriter)

		content := []byte("test")
		_ = tarWriter.WriteHeader(&tar.Header{Name: "file.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(content))})
		_, _ = tarWriter.Write(content)
		_ = tarWriter.Close()
		_ = gzWriter.Close()
		_ = file.Close()

		destDir := t.TempDir()
		result, err := extractBinaryToDir(tarGzPath, BinContentTypeTarGz, destDir)
		if err != nil {
			t.Fatalf("extractBinaryToDir() error = %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result path")
		}
	})

	t.Run("tar.xz dispatches correctly", func(t *testing.T) {
		tmpDir := t.TempDir()

		tarXzPath := filepath.Join(tmpDir, "test.tar.xz")
		file, _ := os.Create(tarXzPath)
		xzWriter, _ := xz.NewWriter(file)
		tarWriter := tar.NewWriter(xzWriter)

		content := []byte("test")
		_ = tarWriter.WriteHeader(&tar.Header{Name: "file.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(content))})
		_, _ = tarWriter.Write(content)
		_ = tarWriter.Close()
		_ = xzWriter.Close()
		_ = file.Close()

		destDir := t.TempDir()
		result, err := extractBinaryToDir(tarXzPath, BinContentTypeTarXz, destDir)
		if err != nil {
			t.Fatalf("extractBinaryToDir() error = %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result path")
		}
	})

	t.Run("unsupported type returns error", func(t *testing.T) {
		_, err := extractBinaryToDir("/dev/null", BinContentTypeBinary, t.TempDir())
		if err == nil {
			t.Error("expected error for unsupported content type, got nil")
		}
	})

	t.Run("tar.bz2 dispatches correctly", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarBz2Path := filepath.Join(tmpDir, "test.tar.bz2")
		createTarBz2(t, tarBz2Path, "file.txt", []byte("test"))

		destDir := t.TempDir()
		result, err := extractBinaryToDir(tarBz2Path, BinContentTypeTarBz2, destDir)
		if err != nil {
			t.Fatalf("extractBinaryToDir(tar.bz2) error = %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result path")
		}
	})

	t.Run("tar.zst dispatches correctly", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarZstPath := filepath.Join(tmpDir, "test.tar.zst")
		createTarZst(t, tarZstPath, "file.txt", []byte("test"))

		destDir := t.TempDir()
		result, err := extractBinaryToDir(tarZstPath, BinContentTypeTarZst, destDir)
		if err != nil {
			t.Fatalf("extractBinaryToDir(tar.zst) error = %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result path")
		}
	})

	t.Run("tar dispatches correctly", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarPath := filepath.Join(tmpDir, "test.tar")
		createPlainTar(t, tarPath, "file.txt", []byte("test"))

		destDir := t.TempDir()
		result, err := extractBinaryToDir(tarPath, BinContentTypeTar, destDir)
		if err != nil {
			t.Fatalf("extractBinaryToDir(tar) error = %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result path")
		}
	})
}

func TestExtractTarBz2(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()
		testContent := []byte("file content in tar.bz2")
		targetPath := "bin/myapp"
		tarBz2Path := filepath.Join(tmpDir, "test.tar.bz2")

		createTarBz2(t, tarBz2Path, targetPath, testContent)

		extractedPath, err := extractTarBz2(tarBz2Path, &targetPath, tmpDir)
		if err != nil {
			t.Fatalf("extractTarBz2() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("binaryPath is nil", func(t *testing.T) {
		_, err := extractTarBz2("test.tar.bz2", nil, t.TempDir())
		if err == nil {
			t.Error("expected error for nil binaryPath, got nil")
		}
	})

	t.Run("file not found in archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarBz2Path := filepath.Join(tmpDir, "test.tar.bz2")
		createTarBz2(t, tarBz2Path, "other/file", []byte("content"))

		targetPath := "bin/myapp"
		_, err := extractTarBz2(tarBz2Path, &targetPath, tmpDir)
		if err == nil {
			t.Error("expected error for file not found, got nil")
		}
	})
}

func TestExtractTarZst(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()
		testContent := []byte("file content in tar.zst")
		targetPath := "bin/myapp"
		tarZstPath := filepath.Join(tmpDir, "test.tar.zst")

		createTarZst(t, tarZstPath, targetPath, testContent)

		extractedPath, err := extractTarZst(tarZstPath, &targetPath, tmpDir)
		if err != nil {
			t.Fatalf("extractTarZst() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("binaryPath is nil", func(t *testing.T) {
		_, err := extractTarZst("test.tar.zst", nil, t.TempDir())
		if err == nil {
			t.Error("expected error for nil binaryPath, got nil")
		}
	})

	t.Run("file not found in archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarZstPath := filepath.Join(tmpDir, "test.tar.zst")
		createTarZst(t, tarZstPath, "other/file", []byte("content"))

		targetPath := "bin/myapp"
		_, err := extractTarZst(tarZstPath, &targetPath, tmpDir)
		if err == nil {
			t.Error("expected error for file not found, got nil")
		}
	})
}

func TestExtractTar(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()
		testContent := []byte("file content in tar")
		targetPath := "bin/myapp"
		tarPath := filepath.Join(tmpDir, "test.tar")

		createPlainTar(t, tarPath, targetPath, testContent)

		extractedPath, err := extractTar(tarPath, &targetPath, tmpDir)
		if err != nil {
			t.Fatalf("extractTar() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("binaryPath is nil", func(t *testing.T) {
		_, err := extractTar("test.tar", nil, t.TempDir())
		if err == nil {
			t.Error("expected error for nil binaryPath, got nil")
		}
	})

	t.Run("file not found in archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		tarPath := filepath.Join(tmpDir, "test.tar")
		createPlainTar(t, tarPath, "other/file", []byte("content"))

		targetPath := "bin/myapp"
		_, err := extractTar(tarPath, &targetPath, tmpDir)
		if err == nil {
			t.Error("expected error for file not found, got nil")
		}
	})
}

func TestExtractBz2(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()
		testContent := []byte("bz2 compressed content")
		bz2Path := filepath.Join(tmpDir, "test.bz2")

		createBz2File(t, bz2Path, testContent)

		extractedPath, err := extractBz2(bz2Path, nil, tmpDir)
		if err != nil {
			t.Fatalf("extractBz2() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		_, err := extractBz2(filepath.Join(t.TempDir(), "nonexistent.bz2"), nil, t.TempDir())
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("invalid bz2 file", func(t *testing.T) {
		tmpDir := t.TempDir()
		bz2Path := filepath.Join(tmpDir, "invalid.bz2")
		if err := os.WriteFile(bz2Path, []byte("not a bz2 file"), 0644); err != nil {
			t.Fatalf("failed to create invalid file: %v", err)
		}

		_, err := extractBz2(bz2Path, nil, tmpDir)
		if err == nil {
			t.Error("expected error for invalid bz2 file, got nil")
		}
	})
}

func TestExtractXz(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()
		testContent := []byte("xz compressed content")
		xzPath := filepath.Join(tmpDir, "test.xz")

		createXzFile(t, xzPath, testContent)

		extractedPath, err := extractXz(xzPath, nil, tmpDir)
		if err != nil {
			t.Fatalf("extractXz() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		_, err := extractXz(filepath.Join(t.TempDir(), "nonexistent.xz"), nil, t.TempDir())
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("invalid xz file", func(t *testing.T) {
		tmpDir := t.TempDir()
		xzPath := filepath.Join(tmpDir, "invalid.xz")
		if err := os.WriteFile(xzPath, []byte("not an xz file"), 0644); err != nil {
			t.Fatalf("failed to create invalid file: %v", err)
		}

		_, err := extractXz(xzPath, nil, tmpDir)
		if err == nil {
			t.Error("expected error for invalid xz file, got nil")
		}
	})
}

func TestExtractZst(t *testing.T) {
	t.Run("successful extraction", func(t *testing.T) {
		tmpDir := t.TempDir()
		testContent := []byte("zstd compressed content")
		zstPath := filepath.Join(tmpDir, "test.zst")

		createZstFile(t, zstPath, testContent)

		extractedPath, err := extractZst(zstPath, nil, tmpDir)
		if err != nil {
			t.Fatalf("extractZst() error = %v", err)
		}

		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		_, err := extractZst(filepath.Join(t.TempDir(), "nonexistent.zst"), nil, t.TempDir())
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("invalid zst file", func(t *testing.T) {
		tmpDir := t.TempDir()
		zstPath := filepath.Join(tmpDir, "invalid.zst")
		if err := os.WriteFile(zstPath, []byte("not a zst file"), 0644); err != nil {
			t.Fatalf("failed to create invalid file: %v", err)
		}

		_, err := extractZst(zstPath, nil, tmpDir)
		if err == nil {
			t.Error("expected error for invalid zst file, got nil")
		}
	})
}

// Helper functions to create test archives

func createTarBz2(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	if _, err := exec.LookPath("bzip2"); err != nil {
		t.Skip("bzip2 command not available")
	}

	tarPath := path + ".tar"
	createPlainTar(t, tarPath, entryName, content)

	cmd := exec.Command("bzip2", "-c", tarPath)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bzip2 command failed: %v", err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatalf("failed to write bz2 file: %v", err)
	}
	_ = os.Remove(tarPath)

	verifyFile, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open bz2 for verification: %v", err)
	}
	defer func() { _ = verifyFile.Close() }()
	bz2Reader := bzip2.NewReader(verifyFile)
	if _, err := io.ReadAll(bz2Reader); err != nil {
		t.Fatalf("created bz2 file is invalid: %v", err)
	}
}

func createTarZst(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create tar.zst file: %v", err)
	}
	zstWriter, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatalf("failed to create zstd writer: %v", err)
	}
	tarWriter := tar.NewWriter(zstWriter)

	header := &tar.Header{
		Name:     entryName,
		Mode:     0755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("failed to write tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("failed to write tar content: %v", err)
	}
	_ = tarWriter.Close()
	_ = zstWriter.Close()
	_ = file.Close()
}

func createPlainTar(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create tar file: %v", err)
	}
	tarWriter := tar.NewWriter(file)

	header := &tar.Header{
		Name:     entryName,
		Mode:     0755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("failed to write tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("failed to write tar content: %v", err)
	}
	_ = tarWriter.Close()
	_ = file.Close()
}

func createBz2File(t *testing.T, path string, content []byte) {
	t.Helper()
	if _, err := exec.LookPath("bzip2"); err != nil {
		t.Skip("bzip2 command not available")
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	cmd := exec.Command("bzip2", "-c", tmpPath)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bzip2 command failed: %v", err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatalf("failed to write bz2 file: %v", err)
	}
	_ = os.Remove(tmpPath)
}

func createXzFile(t *testing.T, path string, content []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create xz file: %v", err)
	}
	xzWriter, err := xz.NewWriter(file)
	if err != nil {
		t.Fatalf("failed to create xz writer: %v", err)
	}
	if _, err := xzWriter.Write(content); err != nil {
		t.Fatalf("failed to write xz content: %v", err)
	}
	_ = xzWriter.Close()
	_ = file.Close()
}

func createZstFile(t *testing.T, path string, content []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create zst file: %v", err)
	}
	zstWriter, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatalf("failed to create zstd writer: %v", err)
	}
	if _, err := zstWriter.Write(content); err != nil {
		t.Fatalf("failed to write zstd content: %v", err)
	}
	_ = zstWriter.Close()
	_ = file.Close()
}

func makeTestTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar write header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractArchiveToPath_InlineBasic(t *testing.T) {
	destDir := t.TempDir()

	tarData := makeTestTar(t, map[string]string{
		"a.txt":     "aaa",
		"sub/b.txt": "bbb",
	})

	result, err := extractArchiveToPath(destDir, tarData, "", BinContentTypeTar)
	if err != nil {
		t.Fatalf("extractArchiveToPath() error = %v", err)
	}
	if result != destDir {
		t.Errorf("expected destDir %q, got %q", destDir, result)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "a.txt"))
	if err != nil {
		t.Fatalf("failed to read a.txt: %v", err)
	}
	if string(content) != "aaa" {
		t.Errorf("a.txt = %q, want %q", string(content), "aaa")
	}

	content, err = os.ReadFile(filepath.Join(destDir, "sub", "b.txt"))
	if err != nil {
		t.Fatalf("failed to read sub/b.txt: %v", err)
	}
	if string(content) != "bbb" {
		t.Errorf("sub/b.txt = %q, want %q", string(content), "bbb")
	}
}

func TestExtractArchiveToPath_FromFile(t *testing.T) {
	destDir := t.TempDir()
	srcDir := t.TempDir()

	tarData := makeTestTar(t, map[string]string{
		"file.txt": "file content",
	})

	tarPath := filepath.Join(srcDir, "test.tar")
	if err := os.WriteFile(tarPath, tarData, 0644); err != nil {
		t.Fatalf("failed to write tar file: %v", err)
	}

	result, err := extractArchiveToPath(destDir, nil, tarPath, BinContentTypeTar)
	if err != nil {
		t.Fatalf("extractArchiveToPath() error = %v", err)
	}
	if result != destDir {
		t.Errorf("expected destDir %q, got %q", destDir, result)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "file.txt"))
	if err != nil {
		t.Fatalf("failed to read file.txt: %v", err)
	}
	if string(content) != "file content" {
		t.Errorf("file.txt = %q, want %q", string(content), "file content")
	}
}

func TestExtractArchiveToPath_PathTraversal(t *testing.T) {
	destDir := t.TempDir()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: "../escape.txt",
		Mode: 0644,
		Size: 4,
	}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()

	_, err := extractArchiveToPath(destDir, buf.Bytes(), "", BinContentTypeTar)
	if err != nil {
		t.Fatalf("extractArchiveToPath() error = %v (expected skip, not error)", err)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(destDir), "escape.txt")); !os.IsNotExist(err) {
		t.Error("path traversal entry should have been skipped")
	}
}

func TestExtractArchiveToPath_SymlinkEscape(t *testing.T) {
	destDir := t.TempDir()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name:     "link",
		Typeflag: tar.TypeSymlink,
		Linkname: "../../etc/passwd",
	}
	_ = tw.WriteHeader(hdr)
	_ = tw.Close()

	_, err := extractArchiveToPath(destDir, buf.Bytes(), "", BinContentTypeTar)
	if err != nil {
		t.Fatalf("extractArchiveToPath() error = %v (expected skip, not error)", err)
	}

	if _, err := os.Lstat(filepath.Join(destDir, "link")); !os.IsNotExist(err) {
		t.Error("symlink escape entry should have been skipped")
	}
}

func TestExtractArchiveToPath_AbsoluteSymlink(t *testing.T) {
	destDir := t.TempDir()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name:     "abslink",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	}
	_ = tw.WriteHeader(hdr)
	_ = tw.Close()

	_, err := extractArchiveToPath(destDir, buf.Bytes(), "", BinContentTypeTar)
	if err != nil {
		t.Fatalf("extractArchiveToPath() error = %v (expected skip, not error)", err)
	}

	if _, err := os.Lstat(filepath.Join(destDir, "abslink")); !os.IsNotExist(err) {
		t.Error("absolute symlink should have been skipped")
	}
}

func TestExtractArchiveToPath_NoDataOrPath(t *testing.T) {
	destDir := t.TempDir()

	_, err := extractArchiveToPath(destDir, nil, "", BinContentTypeTar)
	if err == nil {
		t.Fatal("expected error for nil tarData and empty archivePath")
	}
	if !strings.Contains(err.Error(), "either tarData or archivePath must be provided") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExtractArchiveToPath_ValidSymlink(t *testing.T) {
	destDir := t.TempDir()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "real.txt", Mode: 0644, Size: 5})
	_, _ = tw.Write([]byte("hello"))
	_ = tw.WriteHeader(&tar.Header{Name: "link.txt", Typeflag: tar.TypeSymlink, Linkname: "real.txt"})
	_ = tw.Close()

	_, err := extractArchiveToPath(destDir, buf.Bytes(), "", BinContentTypeTar)
	if err != nil {
		t.Fatalf("extractArchiveToPath() error = %v", err)
	}

	linkTarget, err := os.Readlink(filepath.Join(destDir, "link.txt"))
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if linkTarget != "real.txt" {
		t.Errorf("symlink target = %q, want %q", linkTarget, "real.txt")
	}
}
