package digest

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The XXH3 goldens are the values internal/hashutil pinned before this package
// replaced it: every cache key and store address in the wild is spelled with
// them, so a change here is a silent invalidation of every one of them.

func TestXXH3Of(t *testing.T) {
	t.Run("goldens frozen from hashutil", func(t *testing.T) {
		// hashutil_test.go pinned these; they must never change.
		got := XXH3Of([]byte("hello world")).Hex()
		if got != "df8d09e93f874900a99b8775cc15b6c7" {
			t.Errorf("XXH3Of(hello world) = %s, want df8d09e93f874900a99b8775cc15b6c7", got)
		}
		got = XXH3Multi([]byte("part1"), []byte("part2")).Hex()
		if got != "4b43443fdf4ca7b34d0d576efc8c8ee9" {
			t.Errorf("XXH3Multi(part1, part2) = %s, want 4b43443fdf4ca7b34d0d576efc8c8ee9", got)
		}
	})

	t.Run("nil matches empty", func(t *testing.T) {
		if XXH3Of(nil).Hex() != XXH3Of([]byte{}).Hex() {
			t.Error("nil and empty inputs differ")
		}
	})

	t.Run("canonical string", func(t *testing.T) {
		d := XXH3Of([]byte("hello world"))
		if d.String() != "xxh3:df8d09e93f874900a99b8775cc15b6c7" {
			t.Errorf("String() = %s", d.String())
		}
		if d.Algorithm() != "xxh3" {
			t.Errorf("Algorithm() = %s", d.Algorithm())
		}
	})
}

func TestSHA256Of(t *testing.T) {
	t.Run("known vector", func(t *testing.T) {
		d := SHA256Of([]byte("abc"))
		want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
		if d.Hex() != want {
			t.Errorf("SHA256Of(abc) = %s, want %s", d.Hex(), want)
		}
		if d.String() != "sha256:"+want {
			t.Errorf("String() = %s", d.String())
		}
		if d.Algorithm() != "sha256" {
			t.Errorf("Algorithm() = %s", d.Algorithm())
		}
	})

	t.Run("empty vector", func(t *testing.T) {
		d := SHA256Of(nil)
		want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		if d.Hex() != want {
			t.Errorf("SHA256Of(empty) = %s, want %s", d.Hex(), want)
		}
	})
}

func TestParseStrict(t *testing.T) {
	validSHA := "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	validXXH := "xxh3:df8d09e93f874900a99b8775cc15b6c7"

	t.Run("canonical accepted", func(t *testing.T) {
		d, err := ParseSHA256(validSHA)
		if err != nil || d.Hex() != strings.TrimPrefix(validSHA, "sha256:") {
			t.Fatalf("ParseSHA256(%s) = %v, %v", validSHA, d, err)
		}
		d, err = ParseXXH3(validXXH)
		if err != nil || d.Hex() != strings.TrimPrefix(validXXH, "xxh3:") {
			t.Fatalf("ParseXXH3(%s) = %v, %v", validXXH, d, err)
		}
	})

	t.Run("bare rejected", func(t *testing.T) {
		for _, s := range []string{strings.TrimPrefix(validSHA, "sha256:"), strings.TrimPrefix(validXXH, "xxh3:")} {
			if _, err := ParseSHA256(s); err == nil {
				t.Errorf("ParseSHA256(%s) accepted bare value", s)
			}
			if _, err := ParseXXH3(s); err == nil {
				t.Errorf("ParseXXH3(%s) accepted bare value", s)
			}
		}
	})

	t.Run("wrong algorithm rejected", func(t *testing.T) {
		if _, err := ParseSHA256(validXXH); err == nil {
			t.Error("ParseSHA256 accepted xxh3 digest")
		}
		if _, err := ParseXXH3(validSHA); err == nil {
			t.Error("ParseXXH3 accepted sha256 digest")
		}
	})

	t.Run("uppercase rejected", func(t *testing.T) {
		if _, err := ParseSHA256("sha256:" + strings.ToUpper(strings.TrimPrefix(validSHA, "sha256:"))); err == nil {
			t.Error("ParseSHA256 accepted uppercase value")
		}
	})

	t.Run("wrong length rejected", func(t *testing.T) {
		short := "sha256:" + strings.Repeat("a", 63)
		long := "xxh3:" + strings.Repeat("a", 33)
		if _, err := ParseSHA256(short); err == nil {
			t.Error("ParseSHA256 accepted 63-char value")
		}
		if _, err := ParseXXH3(long); err == nil {
			t.Error("ParseXXH3 accepted 33-char value")
		}
	})

	t.Run("unknown token rejected", func(t *testing.T) {
		if _, err := ParseSHA256("md5:" + strings.Repeat("a", 32)); err == nil {
			t.Error("ParseSHA256 accepted md5 token")
		}
	})
}

func TestParseLoose(t *testing.T) {
	hex64 := "BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD"
	want64 := strings.ToLower(hex64)

	t.Run("uppercase prefix and value", func(t *testing.T) {
		d, err := ParseSHA256Loose("SHA256:" + hex64)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if d.Hex() != want64 || d.String() != "sha256:"+want64 {
			t.Errorf("normalized to %s", d.String())
		}
	})

	t.Run("bare hex by length", func(t *testing.T) {
		d, err := ParseSHA256Loose(" " + hex64 + "\n")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if d.Hex() != want64 {
			t.Errorf("got %s", d.Hex())
		}
		d, err = ParseXXH3Loose("DF8D09E93F874900A99B8775CC15B6C7")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if d.Hex() != "df8d09e93f874900a99b8775cc15b6c7" {
			t.Errorf("got %s", d.Hex())
		}
	})

	t.Run("foreign algorithm token rejected", func(t *testing.T) {
		if _, err := ParseSHA256Loose("md5:" + strings.Repeat("a", 32)); err == nil {
			t.Error("md5 token accepted")
		}
		if _, err := ParseXXH3Loose("sha256:" + want64); err == nil {
			t.Error("sha256 value accepted for xxh3")
		}
	})

	t.Run("bare wrong length rejected", func(t *testing.T) {
		if _, err := ParseSHA256Loose(strings.Repeat("a", 32)); err == nil {
			t.Error("32-hex accepted as sha256")
		}
		if _, err := ParseXXH3Loose(want64); err == nil {
			t.Error("64-hex accepted as xxh3")
		}
	})
}

func TestVerifyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	data := make([]byte, 4096)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("match", func(t *testing.T) {
		if err := SHA256Of(data).VerifyFile(path); err != nil {
			t.Errorf("matching file rejected: %v", err)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		if err := SHA256Of([]byte("other")).VerifyFile(path); err == nil {
			t.Error("mismatching file accepted")
		}
	})

	t.Run("xxh3 cannot verify files", func(t *testing.T) {
		if err := XXH3Of(data).VerifyFile(path); err == nil {
			t.Error("xxh3 VerifyFile succeeded")
		}
	})
}

func TestReaders(t *testing.T) {
	data := bytes.Repeat([]byte("datamitsu"), 10000)

	t.Run("XXH3Reader matches XXH3Of", func(t *testing.T) {
		d, err := XXH3Reader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if d.Hex() != XXH3Of(data).Hex() {
			t.Error("streaming hash differs from one-shot")
		}
	})

	t.Run("SHA256File matches SHA256Of", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "f")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := SHA256File(path)
		if err != nil {
			t.Fatal(err)
		}
		if d.Hex() != SHA256Of(data).Hex() {
			t.Error("file hash differs from one-shot")
		}
	})

	t.Run("SHA256Raw length", func(t *testing.T) {
		sum, err := SHA256Raw(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(sum) != SHA256Of(data).Hex() {
			t.Error("raw sum differs")
		}
	})
}

func TestHexIsPathSafe(t *testing.T) {
	// Hex() feeds filepath joins on every platform; the invariant that matters
	// is that no separator or drive colon can appear in it.
	for _, d := range []Digest{XXH3Of([]byte("x")), SHA256Of([]byte("x"))} {
		h := d.Hex()
		if strings.ContainsAny(h, ":/\\") {
			t.Errorf("Hex() %q contains a path separator", h)
		}
	}
}
