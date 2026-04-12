package runtimemanager

import (
	"strings"
	"testing"
)

func TestCompressDecompressRoundTrip(t *testing.T) {
	original := "lockfileVersion: '9.0'\npackages:\n  '@mermaid-js/mermaid-cli@11.4.2':\n    resolution: {integrity: sha512-abc}\n"

	compressed, err := CompressLockFile(original)
	if err != nil {
		t.Fatalf("CompressLockFile() error = %v", err)
	}

	if !strings.HasPrefix(compressed, brotliPrefix) {
		t.Errorf("compressed should start with %q prefix, got %q", brotliPrefix, compressed[:10])
	}

	if compressed == original {
		t.Error("compressed should differ from original")
	}

	decompressed, err := DecompressLockFile(compressed)
	if err != nil {
		t.Fatalf("DecompressLockFile() error = %v", err)
	}

	if decompressed != original {
		t.Errorf("round-trip failed: got %q, want %q", decompressed, original)
	}
}

func TestDecompressLockFile_PlainTextPassthrough(t *testing.T) {
	plain := "lockfileVersion: '9.0'\nsome content\n"

	result, err := DecompressLockFile(plain)
	if err != nil {
		t.Fatalf("DecompressLockFile() error = %v", err)
	}

	if result != plain {
		t.Errorf("plain text should pass through unchanged: got %q, want %q", result, plain)
	}
}

func TestCompressLockFile_EmptyString(t *testing.T) {
	compressed, err := CompressLockFile("")
	if err != nil {
		t.Fatalf("CompressLockFile() error = %v", err)
	}

	if !strings.HasPrefix(compressed, brotliPrefix) {
		t.Errorf("compressed should start with %q prefix", brotliPrefix)
	}

	decompressed, err := DecompressLockFile(compressed)
	if err != nil {
		t.Fatalf("DecompressLockFile() error = %v", err)
	}

	if decompressed != "" {
		t.Errorf("expected empty string, got %q", decompressed)
	}
}

func TestDecompressLockFile_EmptyString(t *testing.T) {
	result, err := DecompressLockFile("")
	if err != nil {
		t.Fatalf("DecompressLockFile() error = %v", err)
	}

	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestDecompressLockFile_InvalidBase64(t *testing.T) {
	_, err := DecompressLockFile("br:not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestDecompressLockFile_InvalidBrotli(t *testing.T) {
	// Valid base64 but not valid brotli data
	_, err := DecompressLockFile("br:aGVsbG8gd29ybGQ=")
	if err == nil {
		t.Error("expected error for invalid brotli data")
	}
}

func TestDecompressLockFile_ExceedsSizeLimit(t *testing.T) {
	large := strings.Repeat("x", int(maxDecompressedLockFileSize)+1)
	compressed, err := CompressLockFile(large)
	if err != nil {
		t.Fatalf("CompressLockFile() error = %v", err)
	}
	_, err = DecompressLockFile(compressed)
	if err == nil {
		t.Fatal("expected error for oversized decompressed content")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("expected size limit error, got: %v", err)
	}
}

func TestCompressLockFile_LargeContent(t *testing.T) {
	large := strings.Repeat("dependency: version\n", 10000)

	compressed, err := CompressLockFile(large)
	if err != nil {
		t.Fatalf("CompressLockFile() error = %v", err)
	}

	if len(compressed) >= len(large) {
		t.Errorf("compressed (%d bytes) should be smaller than original (%d bytes)", len(compressed), len(large))
	}

	decompressed, err := DecompressLockFile(compressed)
	if err != nil {
		t.Fatalf("DecompressLockFile() error = %v", err)
	}

	if decompressed != large {
		t.Error("round-trip failed for large content")
	}
}
