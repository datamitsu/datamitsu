package hashutil

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestXXH3Hex(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		result := XXH3Hex([]byte{})
		if len(result) != 32 {
			t.Errorf("expected 32 hex chars, got %d: %s", len(result), result)
		}
		if _, err := hex.DecodeString(result); err != nil {
			t.Errorf("invalid hex string: %s", result)
		}
	})

	t.Run("nil input matches empty", func(t *testing.T) {
		nilResult := XXH3Hex(nil)
		emptyResult := XXH3Hex([]byte{})
		if nilResult != emptyResult {
			t.Errorf("nil and empty should produce same hash: %s != %s", nilResult, emptyResult)
		}
	})

	t.Run("golden value", func(t *testing.T) {
		// Pinned golden value to detect accidental changes to hash output encoding.
		result := XXH3Hex([]byte("hello world"))
		if len(result) != 32 {
			t.Fatalf("expected 32 hex chars, got %d: %s", len(result), result)
		}
		// Store the value on first run; if this ever changes, caches will silently break.
		const expected = "df8d09e93f874900a99b8775cc15b6c7"
		if result != expected {
			t.Errorf("golden value mismatch for XXH3Hex(\"hello world\"): got %s, want %s", result, expected)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		input := []byte("hello world")
		a := XXH3Hex(input)
		b := XXH3Hex(input)
		if a != b {
			t.Errorf("not deterministic: %s != %s", a, b)
		}
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		a := XXH3Hex([]byte("hello"))
		b := XXH3Hex([]byte("world"))
		if a == b {
			t.Errorf("different inputs produced same hash: %s", a)
		}
	})
}

func TestXXH3Multi(t *testing.T) {
	t.Run("single part equals XXH3Hex", func(t *testing.T) {
		input := []byte("single")
		a := XXH3Multi(input)
		b := XXH3Hex(input)
		if a != b {
			t.Errorf("single part XXH3Multi should equal XXH3Hex: %s != %s", a, b)
		}
	})

	t.Run("golden value", func(t *testing.T) {
		result := XXH3Multi([]byte("part1"), []byte("part2"))
		const expected = "4b43443fdf4ca7b34d0d576efc8c8ee9"
		if result != expected {
			t.Errorf("golden value mismatch: got %s, want %s", result, expected)
		}
	})

	t.Run("multiple parts are deterministic", func(t *testing.T) {
		a := XXH3Multi([]byte("part1"), []byte("part2"), []byte("part3"))
		b := XXH3Multi([]byte("part1"), []byte("part2"), []byte("part3"))
		if a != b {
			t.Errorf("not deterministic: %s != %s", a, b)
		}
	})

	t.Run("order matters", func(t *testing.T) {
		a := XXH3Multi([]byte("first"), []byte("second"))
		b := XXH3Multi([]byte("second"), []byte("first"))
		if a == b {
			t.Errorf("different order should produce different hash: %s", a)
		}
	})

	t.Run("separator prevents concatenation collision", func(t *testing.T) {
		a := XXH3Multi([]byte("ab"), []byte("cd"))
		b := XXH3Multi([]byte("a"), []byte("bcd"))
		if a == b {
			t.Errorf("separator should prevent collision: %s", a)
		}
	})

	t.Run("empty parts", func(t *testing.T) {
		result := XXH3Multi()
		if len(result) != 32 {
			t.Errorf("expected 32 hex chars for empty input, got %d: %s", len(result), result)
		}
	})

	t.Run("output is 32 hex chars", func(t *testing.T) {
		result := XXH3Multi([]byte("a"), []byte("b"), []byte("c"))
		if len(result) != 32 {
			t.Errorf("expected 32 hex chars, got %d: %s", len(result), result)
		}
	})
}

func TestXXH3Reader(t *testing.T) {
	t.Run("consistent with XXH3Hex for small data", func(t *testing.T) {
		data := []byte("hello world streaming test")
		readerHash, err := XXH3Reader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		directHash := XXH3Hex(data)
		if readerHash != directHash {
			t.Errorf("XXH3Reader should match XXH3Hex: %s != %s", readerHash, directHash)
		}
	})

	t.Run("consistent with XXH3Hex for large data", func(t *testing.T) {
		// Use data larger than the internal block size (typically 256+ bytes)
		// to test streaming across multiple chunks.
		data := make([]byte, 16384)
		if _, err := rand.Read(data); err != nil {
			t.Fatalf("failed to generate random data: %v", err)
		}
		readerHash, err := XXH3Reader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		directHash := XXH3Hex(data)
		if readerHash != directHash {
			t.Errorf("large data: XXH3Reader should match XXH3Hex: %s != %s", readerHash, directHash)
		}
	})

	t.Run("empty reader", func(t *testing.T) {
		result, err := XXH3Reader(bytes.NewReader([]byte{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 32 {
			t.Errorf("expected 32 hex chars, got %d: %s", len(result), result)
		}
		emptyHash := XXH3Hex([]byte{})
		if result != emptyHash {
			t.Errorf("empty reader should match empty XXH3Hex: %s != %s", result, emptyHash)
		}
	})

	t.Run("error propagation", func(t *testing.T) {
		errReader := &failingReader{err: errors.New("read failed")}
		_, err := XXH3Reader(errReader)
		if err == nil {
			t.Error("expected error from failing reader")
		}
		if err.Error() != "read failed" {
			t.Errorf("expected 'read failed', got %q", err.Error())
		}
	})

	t.Run("output is valid hex", func(t *testing.T) {
		result, err := XXH3Reader(bytes.NewReader([]byte("test data")))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := hex.DecodeString(result); err != nil {
			t.Errorf("invalid hex string: %s", result)
		}
	})
}

// failingReader always returns an error on Read.
type failingReader struct {
	err error
}

func (r *failingReader) Read(p []byte) (int, error) {
	return 0, r.err
}

func TestUint128ToBytes(t *testing.T) {
	t.Run("consistency between XXH3Hex and XXH3Reader for known input", func(t *testing.T) {
		// This tests that uint128ToBytes produces identical output whether called
		// from the one-shot path (XXH3Hex) or the streaming path (XXH3Reader).
		data := []byte("consistency check between paths")
		hexResult := XXH3Hex(data)
		readerResult, err := XXH3Reader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hexResult != readerResult {
			t.Errorf("XXH3Hex and XXH3Reader disagree: %s != %s", hexResult, readerResult)
		}
	})
}

// Verify io.Reader interface is satisfied at compile time.
var _ io.Reader = (*failingReader)(nil)
