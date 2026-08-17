package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validOCIRef() *OCIRef {
	return &OCIRef{
		Ref:    "ghcr.io/owner/repo",
		Digest: "sha256:" + strings.Repeat("ab", 32),
	}
}

func TestValidateOCI_NilIsValid(t *testing.T) {
	if err := ValidateOCI(nil); err != nil {
		t.Errorf("ValidateOCI(nil) unexpected error: %v", err)
	}
}

func TestValidateOCI_Valid(t *testing.T) {
	if err := ValidateOCI(validOCIRef()); err != nil {
		t.Errorf("ValidateOCI() unexpected error: %v", err)
	}
}

func TestValidateOCI_ValidWithPortAndLocalhost(t *testing.T) {
	for _, ref := range []string{
		"localhost:5000/owner/repo",
		"registry.example.com:8443/a/b/c",
		"ghcr.io/owner/repo.with-dots_and-dashes",
	} {
		oci := validOCIRef()
		oci.Ref = ref
		if err := ValidateOCI(oci); err != nil {
			t.Errorf("ValidateOCI(ref=%q) unexpected error: %v", ref, err)
		}
	}
}

// TestValidateOCI_SignerIsRejectedAtLoad pins that a pinned signer is a config
// error rather than a no-op. This build has no sigstore dependency and verifies
// nothing, so accepting the field would let a config assert a guarantee the
// binary does not deliver — and the seeder's own rejection only fires once the
// network is already in play. A complete, well-formed signer is the case that
// matters: it is the one a user would expect to work.
func TestValidateOCI_SignerIsRejectedAtLoad(t *testing.T) {
	oci := validOCIRef()
	oci.Signer = &OCISigner{
		Identity: "https://github.com/owner/repo/.github/workflows/release.yml@refs/tags/v1.0.0",
		Issuer:   "https://token.actions.githubusercontent.com",
	}
	err := ValidateOCI(oci)
	if err == nil {
		t.Fatal("ValidateOCI() accepted a signer this build cannot verify")
	}
	for _, want := range []string{"oci: signer", "not implemented in this build", "digest still verifies"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestValidateOCI_MalformedRef(t *testing.T) {
	cases := map[string]string{
		"empty ref":           "",
		"no path component":   "ghcr.io",
		"tag in ref":          "ghcr.io/owner/repo:v1",
		"digest in ref":       "ghcr.io/owner/repo@sha256:" + strings.Repeat("ab", 32),
		"uppercase":           "ghcr.io/Owner/Repo",
		"no registry host":    "owner/repo",
		"scheme prefix":       "https://ghcr.io/owner/repo",
		"whitespace":          "ghcr.io/owner /repo",
		"empty path segment":  "ghcr.io//repo",
		"port without digits": "ghcr.io:/owner/repo",
	}
	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			oci := validOCIRef()
			oci.Ref = ref
			err := ValidateOCI(oci)
			if err == nil {
				t.Fatalf("ValidateOCI(ref=%q) expected error, got nil", ref)
			}
			if !strings.Contains(err.Error(), "oci: ref") {
				t.Errorf("error %q does not mention oci ref", err)
			}
		})
	}
}

func TestValidateOCI_MalformedDigest(t *testing.T) {
	cases := map[string]string{
		"empty digest":     "",
		"no prefix":        strings.Repeat("ab", 32),
		"wrong algorithm":  "sha512:" + strings.Repeat("ab", 32),
		"short hex":        "sha256:" + strings.Repeat("ab", 31),
		"long hex":         "sha256:" + strings.Repeat("ab", 33),
		"uppercase hex":    "sha256:" + strings.Repeat("AB", 32),
		"non-hex tail":     "sha256:" + strings.Repeat("zz", 32),
		"only prefix":      "sha256:",
		"doubled prefix":   "sha256:sha256:" + strings.Repeat("ab", 32),
		"tag-shaped value": "latest",
	}
	for name, digest := range cases {
		t.Run(name, func(t *testing.T) {
			oci := validOCIRef()
			oci.Digest = digest
			err := ValidateOCI(oci)
			if err == nil {
				t.Fatalf("ValidateOCI(digest=%q) expected error, got nil", digest)
			}
			if !strings.Contains(err.Error(), "oci: digest") {
				t.Errorf("error %q does not mention oci digest", err)
			}
		})
	}
}

// TestValidateOCI_AnyShapeOfSignerIsRejected covers the partial spellings too:
// the rejection is about the field being present at all, not about it being
// filled in correctly, so no shape of it may slip through.
func TestValidateOCI_AnyShapeOfSignerIsRejected(t *testing.T) {
	for name, signer := range map[string]*OCISigner{
		"missing issuer":   {Identity: "workflow-ref"},
		"missing identity": {Issuer: "https://token.actions.githubusercontent.com"},
		"both empty":       {},
	} {
		t.Run(name, func(t *testing.T) {
			oci := validOCIRef()
			oci.Signer = signer
			err := ValidateOCI(oci)
			if err == nil {
				t.Fatalf("ValidateOCI(signer=%+v) expected error, got nil", signer)
			}
			if !strings.Contains(err.Error(), "oci: signer") {
				t.Errorf("error %q does not mention oci signer", err)
			}
		})
	}
}

func TestValidateOCI_CollectsAllErrors(t *testing.T) {
	err := ValidateOCI(&OCIRef{Signer: &OCISigner{}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"oci: ref", "oci: digest", "oci: signer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestEmptyConfigMarshalUnchanged pins the execution-cache invalidation key
// contract: a Config with no oci set must marshal exactly as before the field
// existed, otherwise every tool's cache is invalidated on upgrade.
func TestEmptyConfigMarshalUnchanged(t *testing.T) {
	data, err := json.Marshal(Config{})
	if err != nil {
		t.Fatalf("Marshal(Config{}) error: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("Marshal(Config{}) = %s, want {}", data)
	}
}

func TestOCIRefJSONRoundTrip(t *testing.T) {
	in := Config{OCI: &OCIRef{
		Ref:    "ghcr.io/owner/repo",
		Digest: "sha256:" + strings.Repeat("ab", 32),
		Signer: &OCISigner{Identity: "id", Issuer: "iss"},
	}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	want := `{"oci":{"ref":"ghcr.io/owner/repo","digest":"sha256:` + strings.Repeat("ab", 32) + `","signer":{"identity":"id","issuer":"iss"}}}`
	if string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
	var out Config
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if out.OCI == nil || out.OCI.Ref != in.OCI.Ref || out.OCI.Digest != in.OCI.Digest {
		t.Errorf("round trip lost data: %+v", out.OCI)
	}
	if out.OCI.Signer == nil || *out.OCI.Signer != *in.OCI.Signer {
		t.Errorf("round trip lost signer: %+v", out.OCI.Signer)
	}
}

// TestConfigDTSCopiesAreByteIdentical guards the two copies of config.d.ts
// (the repo-root one consumed by user configs and the embedded one) against
// drifting apart. Whoever edits one must copy it over the other.
func TestConfigDTSCopiesAreByteIdentical(t *testing.T) {
	rootCopy, err := os.ReadFile(filepath.Join("..", "..", "config", "config.d.ts"))
	if err != nil {
		t.Fatalf("read repo-root config.d.ts: %v", err)
	}
	if string(rootCopy) != GetDefaultConfigDTS() {
		t.Fatal("config/config.d.ts and internal/config/config.d.ts have diverged; copy one over the other so both are byte-identical")
	}
}
