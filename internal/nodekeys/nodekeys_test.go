package nodekeys

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

func TestReleaseKeyring_LoadsEmbeddedKeys(t *testing.T) {
	kr, err := ReleaseKeyring()
	if err != nil {
		t.Fatalf("ReleaseKeyring() error: %v", err)
	}
	// The bundle from nodejs/release-keys carries many release signing keys;
	// require a healthy lower bound rather than an exact count (keys rotate).
	if len(kr) < 10 {
		t.Fatalf("expected at least 10 release keys, got %d", len(kr))
	}
}

func TestReleaseKeyring_Cached(t *testing.T) {
	kr1, err := ReleaseKeyring()
	if err != nil {
		t.Fatalf("first ReleaseKeyring() error: %v", err)
	}
	kr2, err := ReleaseKeyring()
	if err != nil {
		t.Fatalf("second ReleaseKeyring() error: %v", err)
	}
	if len(kr1) != len(kr2) {
		t.Fatalf("cached keyring differs: %d vs %d", len(kr1), len(kr2))
	}
}

// newSignedManifest clear-signs msg with a freshly generated key and returns
// the armored clearsigned document plus a public keyring containing the signer.
func newSignedManifest(t *testing.T, msg string) (signed []byte, signerRing openpgp.EntityList) {
	t.Helper()
	entity, err := openpgp.NewEntity("Test Signer", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}

	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, entity.PrivateKey, nil)
	if err != nil {
		t.Fatalf("clearsign.Encode: %v", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		t.Fatalf("write message: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close clearsign writer: %v", err)
	}
	return buf.Bytes(), openpgp.EntityList{entity}
}

func TestReadArmoredKeyRings_MissingFooter(t *testing.T) {
	// A buffer that opens a PGP block but never closes it must be rejected: the
	// split loop finds the "-----BEGIN PGP" header, then fails to locate the
	// terminating footer and errors out instead of silently dropping the block.
	data := []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nbody bytes without a closing footer line\n")
	if _, err := readArmoredKeyRings(data); err == nil {
		t.Fatal("expected error for key block missing terminating footer")
	} else if !strings.Contains(err.Error(), "missing terminating footer") {
		t.Errorf("error = %v, want it to mention missing terminating footer", err)
	}
}

func TestReadArmoredKeyRings_MalformedBlock(t *testing.T) {
	// A complete begin/end envelope wrapping garbage (not valid armored key
	// bytes) reaches openpgp.ReadArmoredKeyRing, which must surface a parse
	// error rather than accepting the bogus block.
	data := []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nthis is not valid armored key material\n-----END PGP PUBLIC KEY BLOCK-----\n")
	if _, err := readArmoredKeyRings(data); err == nil {
		t.Fatal("expected error for malformed armored key block")
	} else if !strings.Contains(err.Error(), "reading armored key block") {
		t.Errorf("error = %v, want it to mention reading armored key block", err)
	}
}

func TestReadArmoredKeyRings_ConcatenatedKeys(t *testing.T) {
	// Two freshly generated public keys, serialized as armored blocks and
	// concatenated, must parse into both entities — the whole reason this helper
	// exists (openpgp.ReadArmoredKeyRing alone would consume only the first).
	e1, err := openpgp.NewEntity("Key One", "", "one@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity one: %v", err)
	}
	e2, err := openpgp.NewEntity("Key Two", "", "two@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity two: %v", err)
	}

	armorKey := func(e *openpgp.Entity) []byte {
		var buf bytes.Buffer
		w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
		if err != nil {
			t.Fatalf("armor.Encode: %v", err)
		}
		if err := e.Serialize(w); err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close armor writer: %v", err)
		}
		return buf.Bytes()
	}

	concatenated := append(append([]byte{}, armorKey(e1)...), append([]byte("\n"), armorKey(e2)...)...)
	entities, err := readArmoredKeyRings(concatenated)
	if err != nil {
		t.Fatalf("readArmoredKeyRings concatenated: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("expected 2 entities from concatenated keys, got %d", len(entities))
	}
}

func TestVerifyClearsigned_ValidSignature(t *testing.T) {
	manifest := "abc123  node-v1.2.3-linux-x64.tar.xz\ndef456  node-v1.2.3-win-x64.zip\n"
	signed, ring := newSignedManifest(t, manifest)

	plain, err := VerifyClearsigned(signed, ring)
	if err != nil {
		t.Fatalf("VerifyClearsigned valid: %v", err)
	}
	if !strings.Contains(string(plain), "node-v1.2.3-linux-x64.tar.xz") {
		t.Errorf("verified plaintext missing manifest content: %q", string(plain))
	}
}

func TestVerifyClearsigned_WrongKey(t *testing.T) {
	signed, _ := newSignedManifest(t, "abc123  node-v1.2.3-linux-x64.tar.xz\n")

	// Verify against a keyring that does NOT contain the signer.
	stranger, err := openpgp.NewEntity("Stranger", "", "stranger@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	if _, err := VerifyClearsigned(signed, openpgp.EntityList{stranger}); err == nil {
		t.Fatal("expected error when signer is not in the keyring")
	}
}

func TestVerifyClearsigned_TamperedContent(t *testing.T) {
	manifest := "aaaaaa  node-v1.2.3-linux-x64.tar.xz\n"
	signed, ring := newSignedManifest(t, manifest)

	// Flip a byte in the signed body — the signature should no longer match.
	tampered := bytes.Replace(signed, []byte("aaaaaa"), []byte("bbbbbb"), 1)
	if bytes.Equal(tampered, signed) {
		t.Fatal("test setup failed: content was not modified")
	}
	if _, err := VerifyClearsigned(tampered, ring); err == nil {
		t.Fatal("expected error for tampered clearsigned content")
	}
}

// TestVerifyClearsigned_AppendedContentExcluded refutes the "appended unsigned
// lines verify" concern (review #12): content tacked on AFTER the clearsign
// block's "-----END PGP SIGNATURE-----" footer is outside the signed region, so
// the signature still verifies but the returned plaintext must NOT include the
// appended bytes — otherwise an attacker could graft extra archive lines onto a
// genuinely signed manifest.
func TestVerifyClearsigned_AppendedContentExcluded(t *testing.T) {
	manifest := "abc123  node-v1.2.3-linux-x64.tar.xz\n"
	signed, ring := newSignedManifest(t, manifest)

	const appended = "deadbeef  malware-payload.tar.xz\n"
	tampered := append(append([]byte{}, signed...), []byte(appended)...)

	plain, err := VerifyClearsigned(tampered, ring)
	if err != nil {
		t.Fatalf("VerifyClearsigned with appended trailer: %v", err)
	}
	if strings.Contains(string(plain), "malware-payload.tar.xz") {
		t.Errorf("verified plaintext leaked content appended after the signature: %q", string(plain))
	}
	if !strings.Contains(string(plain), "node-v1.2.3-linux-x64.tar.xz") {
		t.Errorf("verified plaintext lost the genuinely signed manifest line: %q", string(plain))
	}
}

func TestVerifyClearsigned_NotClearsigned(t *testing.T) {
	_, ring := newSignedManifest(t, "x")
	if _, err := VerifyClearsigned([]byte("just some plain text, not a PGP message"), ring); err == nil {
		t.Fatal("expected error when input is not a clearsigned message")
	}
}

func TestVerifyClearsigned_NilKeyring(t *testing.T) {
	signed, _ := newSignedManifest(t, "x")
	if _, err := VerifyClearsigned(signed, nil); err == nil {
		t.Fatal("expected error for nil keyring")
	}
}
