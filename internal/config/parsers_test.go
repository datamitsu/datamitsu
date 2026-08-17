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

func TestValidateParsers_NoSource(t *testing.T) {
	parsers := MapOfParsers{
		"echo": {Hash: validParserHash()},
	}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() expected error for a parser with no source, got nil")
	}
	if !strings.Contains(err.Error(), `parser "echo": exactly one of url or oci is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestValidateParsers_BothSources pins that url and oci do not form a fallback
// chain. The point of the registry source is that an air-gapped organization
// can prove no github.com egress remains; a declaration listing both would
// quietly undo that proof.
func TestValidateParsers_BothSources(t *testing.T) {
	parsers := MapOfParsers{
		"echo": {
			URL:  "https://example.com/echo.wasm",
			Hash: validParserHash(),
			OCI:  validParserOCI(),
		},
	}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() accepted both a url and an oci source")
	}
	if !strings.Contains(err.Error(), `parser "echo": url and oci are mutually exclusive`) {
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
		"c": {Hash: validParserHash(), OCI: &ParserOCI{Ref: "NoHost", Digest: "latest"}},
	}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() expected error, got nil")
	}
	for _, want := range []string{
		`parser "a": exactly one of url or oci is required`,
		`parser "a": hash is required`,
		`parser "b": hash must be a valid SHA-256 hex string`,
		`parser "c": oci.ref "NoHost" is not a valid repository reference`,
		`parser "c": oci.digest "latest" must be "sha256:"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}

// validParserOCI returns a well-formed registry source for a parser.
func validParserOCI() *ParserOCI {
	return &ParserOCI{
		Ref:    "ghcr.io/datamitsu/datamitsu-parsers",
		Digest: "sha256:" + strings.Repeat("cd", 32),
	}
}

func TestValidateParsers_ValidOCISource(t *testing.T) {
	for _, ref := range []string{
		"ghcr.io/datamitsu/datamitsu-parsers",
		"localhost:5000/dm/parsers",
		"harbor.corp.example/dm/datamitsu-parsers",
	} {
		oci := validParserOCI()
		oci.Ref = ref
		parsers := MapOfParsers{"core": {Hash: validParserHash(), OCI: oci}}
		if err := ValidateParsers(parsers); err != nil {
			t.Errorf("ValidateParsers(ref=%q) unexpected error: %v", ref, err)
		}
	}
}

// TestValidateParsers_OCIHashStaysMandatory is the security-policy case: the
// registry digest chain is added integrity, never a substitute for the config
// hash. Without the hash there is nothing tying the artifact to the config, and
// nothing for the layer digest to be checked against.
func TestValidateParsers_OCIHashStaysMandatory(t *testing.T) {
	parsers := MapOfParsers{"core": {OCI: validParserOCI()}}
	err := ValidateParsers(parsers)
	if err == nil {
		t.Fatal("ValidateParsers() accepted an oci parser with no hash")
	}
	if !strings.Contains(err.Error(), `parser "core": hash is required (SHA-256)`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateParsers_MalformedOCIRef(t *testing.T) {
	cases := map[string]string{
		"empty ref":          "",
		"no path component":  "ghcr.io",
		"tag in ref":         "ghcr.io/datamitsu/datamitsu-parsers:v1",
		"digest in ref":      "ghcr.io/dm/parsers@sha256:" + strings.Repeat("ab", 32),
		"uppercase":          "ghcr.io/Datamitsu/Parsers",
		"no registry host":   "datamitsu/parsers",
		"scheme prefix":      "https://ghcr.io/dm/parsers",
		"empty path segment": "ghcr.io//parsers",
	}
	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			oci := validParserOCI()
			oci.Ref = ref
			err := ValidateParsers(MapOfParsers{"core": {Hash: validParserHash(), OCI: oci}})
			if err == nil {
				t.Fatalf("ValidateParsers(ref=%q) expected error, got nil", ref)
			}
			if !strings.Contains(err.Error(), `parser "core": oci.ref`) {
				t.Errorf("error %q does not name the parser's oci.ref", err)
			}
		})
	}
}

// TestValidateParsers_MalformedOCIRefDiscriminatesHost pins the two-branch
// message contract: a reference that is merely missing its registry host gets
// told exactly that, instead of a generic syntax complaint it cannot act on.
func TestValidateParsers_MalformedOCIRefDiscriminatesHost(t *testing.T) {
	oci := validParserOCI()
	oci.Ref = "datamitsu/parsers"
	err := ValidateParsers(MapOfParsers{"core": {Hash: validParserHash(), OCI: oci}})
	if err == nil {
		t.Fatal("expected error for a hostless ref")
	}
	if !strings.Contains(err.Error(), "must include the registry host as its first segment") {
		t.Errorf("error %q does not point at the missing host", err)
	}
}

func TestValidateParsers_MalformedOCIDigest(t *testing.T) {
	cases := map[string]string{
		"empty digest":     "",
		"no prefix":        strings.Repeat("ab", 32),
		"wrong algorithm":  "sha512:" + strings.Repeat("ab", 32),
		"short hex":        "sha256:" + strings.Repeat("ab", 31),
		"uppercase hex":    "sha256:" + strings.Repeat("AB", 32),
		"non-hex tail":     "sha256:" + strings.Repeat("zz", 32),
		"only prefix":      "sha256:",
		"tag-shaped value": "latest",
	}
	for name, digest := range cases {
		t.Run(name, func(t *testing.T) {
			oci := validParserOCI()
			oci.Digest = digest
			err := ValidateParsers(MapOfParsers{"core": {Hash: validParserHash(), OCI: oci}})
			if err == nil {
				t.Fatalf("ValidateParsers(digest=%q) expected error, got nil", digest)
			}
			if !strings.Contains(err.Error(), `parser "core": oci.digest`) {
				t.Errorf("error %q does not name the parser's oci.digest", err)
			}
		})
	}
}

func TestValidateParsers_OCISignerIsRejected(t *testing.T) {
	oci := validParserOCI()
	oci.Signer = &OCISigner{
		Identity: "https://github.com/datamitsu/datamitsu/.github/workflows/release.yml@refs/heads/main",
		Issuer:   "https://token.actions.githubusercontent.com",
	}
	err := ValidateParsers(MapOfParsers{"core": {Hash: validParserHash(), OCI: oci}})
	if err == nil {
		t.Fatal("ValidateParsers() accepted a signer this build cannot verify")
	}
	for _, want := range []string{`parser "core": oci.signer`, "not implemented in this build", "hash still verifies"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestConfig_ParserOCIRoundTripsThroughJSON covers the declaration a wrapper's
// OCI config variant actually emits.
func TestConfig_ParserOCIRoundTripsThroughJSON(t *testing.T) {
	jsonStr := `{
		"parsers": {
			"core": {
				"hash": "` + validParserHash() + `",
				"oci": {"ref": "ghcr.io/datamitsu/datamitsu-parsers", "digest": "sha256:` + strings.Repeat("cd", 32) + `"}
			}
		}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	p := cfg.Parsers["core"]
	if p.OCI == nil {
		t.Fatal("parser oci source was dropped")
	}
	if p.OCI.Ref != "ghcr.io/datamitsu/datamitsu-parsers" || p.OCI.Digest != "sha256:"+strings.Repeat("cd", 32) {
		t.Errorf("unexpected oci values: %+v", p.OCI)
	}
	if p.URL != "" {
		t.Errorf("url should stay empty for an oci-sourced parser, got %q", p.URL)
	}
	if err := ValidateParsers(cfg.Parsers); err != nil {
		t.Errorf("valid oci parsers config failed validation: %v", err)
	}
}

// TestConfig_URLParserMarshalUnchanged is the upgrade-safety case. The whole
// Config is marshaled into the execution-cache invalidation key, so a url-only
// parser has to serialize exactly as it did before `oci` and `omitempty`
// existed — otherwise every user's tool cache is invalidated on upgrade.
func TestConfig_URLParserMarshalUnchanged(t *testing.T) {
	cfg := Config{Parsers: MapOfParsers{
		"core": {URL: "https://example.com/core.wasm", Hash: validParserHash()},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	want := `{"parsers":{"core":{"url":"https://example.com/core.wasm","hash":"` + validParserHash() + `"}}}`
	if string(data) != want {
		t.Errorf("url-only parser marshaling moved:\n got: %s\nwant: %s", data, want)
	}
}
