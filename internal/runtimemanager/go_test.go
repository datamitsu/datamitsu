package runtimemanager

import (
	"strings"
	"testing"
)

func TestParseGoLockFile_Valid(t *testing.T) {
	goMod := "module datamitsu-govulncheck\n\ngo 1.22\n\nrequire golang.org/x/vuln v1.1.4\n"
	goSum := "golang.org/x/vuln v1.1.4 h1:abc=\ngolang.org/x/vuln v1.1.4/go.mod h1:def=\n"

	jsonStr, err := buildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("buildGoLockFileJSON() error = %v", err)
	}

	gotMod, gotSum, err := parseGoLockFile(jsonStr)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}

	if gotMod != goMod {
		t.Errorf("go.mod mismatch: got %q, want %q", gotMod, goMod)
	}
	if gotSum != goSum {
		t.Errorf("go.sum mismatch: got %q, want %q", gotSum, goSum)
	}
}

func TestParseGoLockFile_InvalidJSON(t *testing.T) {
	_, _, err := parseGoLockFile("this is not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseGoLockFile_MalformedJSON(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "x", "sum": `)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseGoLockFile_MissingMod(t *testing.T) {
	_, _, err := parseGoLockFile(`{"sum": "golang.org/x/vuln v1.1.4 h1:abc=\n"}`)
	if err == nil {
		t.Error("expected error when mod field is missing")
	}
	if err != nil && !strings.Contains(err.Error(), "mod") {
		t.Errorf("expected error to mention mod field, got: %v", err)
	}
}

func TestParseGoLockFile_MissingSum(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "module datamitsu-x\n\ngo 1.22\n"}`)
	if err == nil {
		t.Error("expected error when sum field is missing")
	}
	if err != nil && !strings.Contains(err.Error(), "sum") {
		t.Errorf("expected error to mention sum field, got: %v", err)
	}
}

func TestParseGoLockFile_EmptyMod(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "", "sum": "x"}`)
	if err == nil {
		t.Error("expected error when mod field is empty")
	}
}

func TestParseGoLockFile_EmptySum(t *testing.T) {
	_, _, err := parseGoLockFile(`{"mod": "module x\ngo 1.22\n", "sum": ""}`)
	if err == nil {
		t.Error("expected error when sum field is empty")
	}
}

func TestBuildGoLockFileJSON_ValidJSON(t *testing.T) {
	goMod := "module datamitsu-x\n\ngo 1.22\n"
	goSum := "golang.org/x/vuln v1.1.4 h1:abc=\n"

	jsonStr, err := buildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("buildGoLockFileJSON() error = %v", err)
	}

	if !strings.Contains(jsonStr, `"mod"`) {
		t.Errorf("expected JSON to contain mod field, got %q", jsonStr)
	}
	if !strings.Contains(jsonStr, `"sum"`) {
		t.Errorf("expected JSON to contain sum field, got %q", jsonStr)
	}

	// Round-trip through parse to confirm well-formed JSON.
	gotMod, gotSum, err := parseGoLockFile(jsonStr)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}
	if gotMod != goMod || gotSum != goSum {
		t.Errorf("round-trip mismatch: got mod=%q sum=%q, want mod=%q sum=%q", gotMod, gotSum, goMod, goSum)
	}
}

func TestBuildGoLockFileJSON_PreservesSpecialChars(t *testing.T) {
	// go.mod / go.sum content includes newlines, slashes, quotes-free but
	// JSON escaping must still round-trip exactly.
	goMod := "module example.com/x\n\ngo 1.22\n\nrequire (\n\tgolang.org/x/tools v0.1.0\n)\n"
	goSum := "golang.org/x/tools v0.1.0 h1:abc/def+ghi=\ngolang.org/x/tools v0.1.0/go.mod h1:xyz=\n"

	jsonStr, err := buildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("buildGoLockFileJSON() error = %v", err)
	}

	gotMod, gotSum, err := parseGoLockFile(jsonStr)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}
	if gotMod != goMod {
		t.Errorf("go.mod not preserved: got %q, want %q", gotMod, goMod)
	}
	if gotSum != goSum {
		t.Errorf("go.sum not preserved: got %q, want %q", gotSum, goSum)
	}
}

func TestGoLockFile_BuildCompressDecompressParseRoundTrip(t *testing.T) {
	goMod := "module datamitsu-govulncheck\n\ngo 1.22\n\nrequire golang.org/x/vuln v1.1.4\n"
	goSum := strings.Repeat("golang.org/x/vuln v1.1.4 h1:AAAA=\ngolang.org/x/vuln v1.1.4/go.mod h1:BBBB=\n", 50)

	jsonStr, err := buildGoLockFileJSON(goMod, goSum)
	if err != nil {
		t.Fatalf("buildGoLockFileJSON() error = %v", err)
	}

	compressed, err := CompressLockFile(jsonStr)
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

	gotMod, gotSum, err := parseGoLockFile(decompressed)
	if err != nil {
		t.Fatalf("parseGoLockFile() error = %v", err)
	}

	if gotMod != goMod {
		t.Errorf("round-trip go.mod mismatch: got %q, want %q", gotMod, goMod)
	}
	if gotSum != goSum {
		t.Errorf("round-trip go.sum mismatch: got %q, want %q", gotSum, goSum)
	}
}
