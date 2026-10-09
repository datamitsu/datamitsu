package binmanager

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestVerifyFileHashPublicAlgorithmPolicy(t *testing.T) {
	// Security policy: download verification is SHA-256 only. A pin naming any
	// other algorithm — even a valid digest of that algorithm — is refused.
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{
		"md5:d41d8cd98f00b204e9800998ecf8427e",
		"sha1:a9993e364706816aba3e25717850c26c9cd0d89d",
		"sha512:" + strings.Repeat("a", 128),
	} {
		if err := VerifyFileHashPublic(path, pin); err == nil {
			t.Errorf("VerifyFileHashPublic(%q) = nil, want algorithm-refused error", pin)
		}
	}
}

func TestVerifyFileHashPublic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	content := []byte("verifiable content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := digest.SHA256Of(content)
	good := sum.Hex()

	t.Run("matching hash succeeds", func(t *testing.T) {
		if err := VerifyFileHashPublic(path, good); err != nil {
			t.Errorf("VerifyFileHashPublic() error = %v, want nil", err)
		}
	})

	t.Run("wrong hash fails", func(t *testing.T) {
		bad := "0000000000000000000000000000000000000000000000000000000000000000"
		if err := VerifyFileHashPublic(path, bad); err == nil {
			t.Error("VerifyFileHashPublic() with wrong hash = nil, want error")
		}
	})

	t.Run("missing file fails", func(t *testing.T) {
		if err := VerifyFileHashPublic(filepath.Join(dir, "nope"), good); err == nil {
			t.Error("VerifyFileHashPublic() on missing file = nil, want error")
		}
	})
}

func TestExtractDirForVerify(t *testing.T) {
	t.Run("extracts a zip to a directory", func(t *testing.T) {
		dir := t.TempDir()
		zipPath := filepath.Join(dir, "bundle.zip")

		file, err := os.Create(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(file)
		// A nested file plus a top-level file exercise the dir-creation path.
		for name, body := range map[string]string{
			"bin/tool":  "#!/bin/sh\necho hi\n",
			"README.md": "docs",
		} {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		destDir := t.TempDir()
		outDir, err := ExtractDirForVerify(zipPath, BinContentTypeZip, destDir)
		if err != nil {
			t.Fatalf("ExtractDirForVerify() error = %v", err)
		}
		got, err := os.ReadFile(filepath.Join(outDir, "bin", "tool"))
		if err != nil {
			t.Fatalf("extracted file missing: %v", err)
		}
		if string(got) != "#!/bin/sh\necho hi\n" {
			t.Errorf("extracted content = %q", string(got))
		}
	})

	t.Run("rejects a single-binary content type", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := ExtractDirForVerify(filepath.Join(dir, "x"), BinContentTypeBinary, dir); err == nil {
			t.Error("ExtractDirForVerify(binary) = nil, want unsupported-content-type error")
		}
	})
}
