package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfig_ParsersJSONRoundTrip(t *testing.T) {
	jsonStr := `{
		"parsers": {
			"echo": {"url": "https://example.com/echo.wasm", "hash": "` + validParserHash() + `"}
		}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	p, ok := cfg.Parsers["echo"]
	if !ok {
		t.Fatal("expected parser \"echo\" to be present")
	}
	if p.URL != "https://example.com/echo.wasm" || p.Hash != validParserHash() {
		t.Errorf("unexpected parser values: %+v", p)
	}
	if err := ValidateParsers(cfg.Parsers); err != nil {
		t.Errorf("valid parsers config failed validation: %v", err)
	}
}

// TestConfig_ParsersIgnoresLegacyVersion documents that a stray `version` key
// (removed from the Parser entity — the module reports its own version via its
// WASM `describe` export) is silently ignored, so an older config still parses.
func TestConfig_ParsersIgnoresLegacyVersion(t *testing.T) {
	jsonStr := `{"parsers": {"echo": {"url": "https://x/echo.wasm", "hash": "` + validParserHash() + `", "version": "1.0.0"}}}`
	var cfg Config
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if _, ok := cfg.Parsers["echo"]; !ok {
		t.Fatal("expected parser \"echo\" to be present despite legacy version key")
	}
}

func TestConfig_ParsersOmitEmpty(t *testing.T) {
	data, err := json.Marshal(Config{})
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	if strings.Contains(string(data), "parsers") {
		t.Errorf("empty Config should omit parsers field; got: %s", data)
	}
}

func validParserHash() string {
	return strings.Repeat("ab", 32) // 64 lowercase hex chars
}

func TestValidateParsers_NilAndEmptyAreValid(t *testing.T) {
	if err := ValidateParsers(nil); err != nil {
		t.Errorf("ValidateParsers(nil) unexpected error: %v", err)
	}
	if err := ValidateParsers(MapOfParsers{}); err != nil {
		t.Errorf("ValidateParsers(empty) unexpected error: %v", err)
	}
}

func TestValidateParsers_Valid(t *testing.T) {
	parsers := MapOfParsers{
		"echo": {URL: "https://example.com/echo.wasm", Hash: validParserHash()},
		"bare": {URL: "https://example.com/x.wasm", Hash: validParserHash()},
	}
	if err := ValidateParsers(parsers); err != nil {
		t.Errorf("ValidateParsers() unexpected error: %v", err)
	}
}

func TestValidateParsers_MissingURL(t *testing.T) {
	parsers := MapOfParsers{
		"echo": {URL: "", Hash: validParserHash()},
	}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() expected error for missing url, got nil")
	}
	if !strings.Contains(err.Error(), `parser "echo": url is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateParsers_MissingHash(t *testing.T) {
	parsers := MapOfParsers{
		"echo": {URL: "https://example.com/echo.wasm", Hash: ""},
	}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() expected error for missing hash, got nil")
	}
	if !strings.Contains(err.Error(), `parser "echo": hash is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateParsers_MalformedHash(t *testing.T) {
	cases := map[string]string{
		"too short":   strings.Repeat("ab", 16),
		"too long":    strings.Repeat("ab", 40),
		"uppercase":   strings.ToUpper(validParserHash()),
		"non-hex":     strings.Repeat("zz", 32),
		"with prefix": "sha256:" + validParserHash(),
	}
	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			parsers := MapOfParsers{
				"echo": {URL: "https://example.com/echo.wasm", Hash: hash},
			}
			err := ValidateParsers(parsers)
			if err == nil {
				t.Fatalf("ValidateParsers() expected error for %s hash, got nil", name)
			}
			if !strings.Contains(err.Error(), "must be a valid SHA-256 hex string") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestValidateParsers_EmptyName(t *testing.T) {
	parsers := MapOfParsers{
		"": {URL: "https://example.com/echo.wasm", Hash: validParserHash()},
	}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() expected error for empty name, got nil")
	}
	if !strings.Contains(err.Error(), "parser name must not be empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateParsers_AggregatesAllErrors(t *testing.T) {
	parsers := MapOfParsers{
		"a": {URL: "", Hash: ""},
		"b": {URL: "https://example.com/b.wasm", Hash: "bad"},
	}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() expected error, got nil")
	}
	for _, want := range []string{
		`parser "a": url is required`,
		`parser "a": hash is required`,
		`parser "b": hash must be a valid SHA-256 hex string`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}
