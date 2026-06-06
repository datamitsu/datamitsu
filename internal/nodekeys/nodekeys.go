// Package nodekeys provides the Node.js release signing keys and helpers to
// verify the GPG signature on Node.js release manifests (SHASUMS256.txt.asc).
//
// This is a devtools-time provenance check: when the `pull-node` registry
// generator records SHA-256 hashes for glibc Node archives, it first verifies
// that the SHASUMS256.txt manifest was signed by an official Node.js release
// key. The git-pinned hash remains the runtime security anchor; this check
// strengthens the supply-chain story at pull time.
package nodekeys

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

// embeddedReleaseKeys holds the armored public keys of the Node.js release
// team, sourced from https://github.com/nodejs/release-keys (keys/*.asc).
//
//go:embed node_release_keys.asc
var embeddedReleaseKeys []byte

var (
	releaseKeyringOnce sync.Once
	releaseKeyring     openpgp.EntityList
	releaseKeyringErr  error
)

// ReleaseKeyring returns the embedded Node.js release signing keys parsed into
// an openpgp keyring. The keyring is parsed once and cached.
func ReleaseKeyring() (openpgp.EntityList, error) {
	releaseKeyringOnce.Do(func() {
		releaseKeyring, releaseKeyringErr = readArmoredKeyRings(embeddedReleaseKeys)
		if releaseKeyringErr != nil {
			releaseKeyringErr = fmt.Errorf("parsing embedded Node.js release keys: %w", releaseKeyringErr)
		} else if len(releaseKeyring) == 0 {
			releaseKeyringErr = errors.New("embedded Node.js release keyring is empty")
		}
	})
	return releaseKeyring, releaseKeyringErr
}

// pgpPublicKeyEnd is the armor footer terminating one ASCII-armored public key
// block. The Node.js release-keys bundle concatenates many such blocks, and
// openpgp.ReadArmoredKeyRing only consumes the first one, so we split and parse
// each block individually.
const (
	pgpPublicKeyBegin = "-----BEGIN PGP"
	pgpPublicKeyEnd   = "-----END PGP PUBLIC KEY BLOCK-----"
)

// readArmoredKeyRings parses a buffer containing one or more concatenated
// ASCII-armored public key blocks into a single EntityList.
func readArmoredKeyRings(data []byte) (openpgp.EntityList, error) {
	var all openpgp.EntityList
	rest := data
	for {
		begin := bytes.Index(rest, []byte(pgpPublicKeyBegin))
		if begin < 0 {
			break
		}
		rest = rest[begin:]
		end := bytes.Index(rest, []byte(pgpPublicKeyEnd))
		if end < 0 {
			return nil, errors.New("armored key block missing terminating footer")
		}
		blockEnd := end + len(pgpPublicKeyEnd)
		entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(rest[:blockEnd]))
		if err != nil {
			return nil, fmt.Errorf("reading armored key block: %w", err)
		}
		all = append(all, entities...)
		rest = rest[blockEnd:]
	}
	return all, nil
}

// VerifyClearsigned verifies a clearsigned document — the format used by
// nodejs.org's SHASUMS256.txt.asc — against the given keyring. On success it
// returns the verified plaintext (the bytes covered by the signature).
//
// Callers MUST use the returned bytes as the source of truth; reading a
// separately fetched copy of the manifest would defeat the verification. Only
// the bytes inside the clearsign block are returned, so any content appended
// after the "-----END PGP SIGNATURE-----" footer is excluded by construction —
// unsigned trailing lines can never ride along as "verified".
//
// Trust model (devtools pull-time only): this establishes signature VALIDITY —
// that some key in the supplied keyring signed exactly these bytes. It
// deliberately does NOT enforce key revocation, key expiry, or
// trust-level/web-of-trust policy, and a successful verification is not by
// itself the runtime security anchor. The authority for what datamitsu actually
// downloads at runtime is the git-pinned SHA-256 recorded in runtimes.json; this
// GPG check only strengthens the supply-chain provenance story at the moment
// those hashes are first generated.
func VerifyClearsigned(data []byte, keyring openpgp.KeyRing) ([]byte, error) {
	if keyring == nil {
		return nil, errors.New("nil keyring")
	}
	block, _ := clearsign.Decode(data)
	if block == nil {
		return nil, errors.New("no clearsigned PGP message found")
	}
	if _, err := openpgp.CheckDetachedSignature(
		keyring,
		bytes.NewReader(block.Bytes),
		block.ArmoredSignature.Body,
		nil,
	); err != nil {
		return nil, fmt.Errorf("clearsign signature verification failed: %w", err)
	}
	return block.Bytes, nil
}
