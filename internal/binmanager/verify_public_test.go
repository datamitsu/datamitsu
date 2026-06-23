package binmanager

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestIsAllowedDownloadHashType(t *testing.T) {
	// Security policy: only SHA-256 is permitted for download verification.
	allowed := []BinHashType{BinHashTypeSHA256}
	denied := []BinHashType{BinHashTypeSHA1, BinHashTypeSHA384, BinHashTypeSHA512, BinHashTypeMD5, "", "crc32"}

	for _, ht := range allowed {
		if !IsAllowedDownloadHashType(ht) {
			t.Errorf("IsAllowedDownloadHashType(%q) = false, want true", ht)
		}
	}
	for _, ht := range denied {
		if IsAllowedDownloadHashType(ht) {
			t.Errorf("IsAllowedDownloadHashType(%q) = true, want false", ht)
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
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	t.Run("matching hash succeeds", func(t *testing.T) {
		if err := VerifyFileHashPublic(path, good, BinHashTypeSHA256); err != nil {
			t.Errorf("VerifyFileHashPublic() error = %v, want nil", err)
		}
	})

	t.Run("wrong hash fails", func(t *testing.T) {
		bad := "0000000000000000000000000000000000000000000000000000000000000000"
		if err := VerifyFileHashPublic(path, bad, BinHashTypeSHA256); err == nil {
			t.Error("VerifyFileHashPublic() with wrong hash = nil, want error")
		}
	})

	t.Run("missing file fails", func(t *testing.T) {
		if err := VerifyFileHashPublic(filepath.Join(dir, "nope"), good, BinHashTypeSHA256); err == nil {
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
