// Package digest owns every hash datamitsu computes or verifies: XXH3-128 for
// internal keys and fingerprints, SHA-256 for everything that arrives from, or
// is compared against, the outside world (Hashing Policy in AGENTS.md).
//
// A digest has one canonical string form, "alg:value" — algorithm token, a
// colon, then the lowercase hex value — used wherever a person reads, writes or
// copies a hash: config pins, generated manifests, error messages, committed
// fingerprints. Hash inputs and cache/store addresses use the bare value
// (Digest.Hex) instead: an identity must not depend on how a pin was spelled,
// and a colon cannot appear in a Windows path component.
//
// The algorithm set is closed. There is no Parse(alg, …) and no exported
// Algorithm type: each entry point names the algorithm it expects, so a wrong
// algorithm is a parse error at the boundary, and an unsupported one is a
// compile error.
package digest

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"

	"github.com/zeebo/xxh3"
)

// algorithm is the closed set of supported algorithms.
type algorithm string

const (
	sha256Alg algorithm = "sha256" // value: 64 lowercase hex characters
	xxh3Alg   algorithm = "xxh3"   // XXH3-128; value: 32 lowercase hex characters
)

func (a algorithm) hexLen() int {
	if a == sha256Alg {
		return 64
	}
	return 32
}

// Digest is an immutable verified digest. It exists at integrity boundaries —
// parsing a declared pin, verifying bytes, comparing digests — never as a field
// of a struct that crosses msgpack, goja or any persisted Go value: those carry
// canonical or bare strings as their format decides.
type Digest struct {
	alg   algorithm
	value string // lowercase hex, exact length for alg
}

// String returns the canonical "alg:value" form: pins, display, person-facing
// persistence.
func (d Digest) String() string { return string(d.alg) + ":" + d.value }

// Hex returns the bare lowercase hex value: hash inputs and cache/store
// addresses. It never contains a colon.
func (d Digest) Hex() string { return d.value }

// Algorithm returns the algorithm token, "sha256" or "xxh3".
func (d Digest) Algorithm() string { return string(d.alg) }

// SHA256Of builds the SHA-256 digest of data.
func SHA256Of(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest{alg: sha256Alg, value: hex.EncodeToString(sum[:])}
}

// XXH3Of builds the XXH3-128 digest of data.
func XXH3Of(data []byte) Digest {
	var buf [16]byte
	put128(&buf, xxh3.Hash128(data))
	return Digest{alg: xxh3Alg, value: hex.EncodeToString(buf[:])}
}

// XXH3Multi joins parts with a single NUL separator and returns the XXH3-128
// digest. Byte-identical to the hashutil.XXH3Multi it replaces.
func XXH3Multi(parts ...[]byte) Digest {
	size := 0
	for i, p := range parts {
		if i > 0 {
			size++ // separator
		}
		size += len(p)
	}
	buf := make([]byte, 0, size)
	for i, p := range parts {
		if i > 0 {
			buf = append(buf, 0)
		}
		buf = append(buf, p...)
	}
	return XXH3Of(buf)
}

// SHA256Raw streams r and returns the raw 32-byte SHA-256 sum, for callers that
// hash while copying and compare bytes themselves.
func SHA256Raw(r io.Reader) ([]byte, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("read data for hashing: %w", err)
	}
	return h.Sum(nil), nil
}

// XXH3Reader streams r and returns its XXH3-128 digest.
func XXH3Reader(r io.Reader) (Digest, error) {
	h := xxh3.New128()
	if _, err := io.Copy(h, r); err != nil {
		return Digest{}, fmt.Errorf("read data for hashing: %w", err)
	}
	var buf [16]byte
	put128(&buf, h.Sum128())
	return Digest{alg: xxh3Alg, value: hex.EncodeToString(buf[:])}, nil
}

// SHA256File streams the file at path and returns its SHA-256 digest.
func SHA256File(path string) (Digest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Digest{}, fmt.Errorf("open %s for hashing: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sum, err := SHA256Raw(f)
	if err != nil {
		return Digest{}, err
	}
	return Digest{alg: sha256Alg, value: hex.EncodeToString(sum)}, nil
}

// VerifyFile streams the file at path and reports whether its SHA-256 matches.
// The comparison is constant-time over the hex values.
func (d Digest) VerifyFile(path string) error {
	if d.alg != sha256Alg {
		return fmt.Errorf("verify file: unsupported algorithm %q", d.alg)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s for verification: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sum, err := SHA256Raw(f)
	if err != nil {
		return err
	}
	actual := hex.EncodeToString(sum)
	if subtle.ConstantTimeCompare([]byte(actual), []byte(d.value)) != 1 {
		return fmt.Errorf("hash mismatch: expected %s, got sha256:%s", d, actual)
	}
	return nil
}

// ParseSHA256 parses the canonical form "sha256:<64 lowercase hex>". It is the
// parser for pins datamitsu itself defines: config fields, manifest entries.
func ParseSHA256(s string) (Digest, error) {
	return parseStrict(sha256Alg, s)
}

// ParseXXH3 parses the canonical form "xxh3:<32 lowercase hex>". It is the
// parser for chain-hash pins and committed internal fingerprints.
func ParseXXH3(s string) (Digest, error) {
	return parseStrict(xxh3Alg, s)
}

// ParseSHA256Loose parses what a person or an upstream feed hands over: the
// canonical form in any case, or a bare 64-hex value. The result is always
// canonical. Use it only where the source is external — hand-written pin maps,
// release pages, checksum files, getRemoteConfigs entries.
func ParseSHA256Loose(s string) (Digest, error) {
	return parseLoose(sha256Alg, s)
}

// ParseXXH3Loose is ParseSHA256Loose for a 32-hex XXH3-128 value.
func ParseXXH3Loose(s string) (Digest, error) {
	return parseLoose(xxh3Alg, s)
}

func parseStrict(alg algorithm, s string) (Digest, error) {
	prefix := string(alg) + ":"
	if len(s) <= len(prefix) || s[:len(prefix)] != prefix {
		return Digest{}, fmt.Errorf("invalid %s digest %q: expected %s:<%d lowercase hex characters>", alg, s, alg, alg.hexLen())
	}
	value := s[len(prefix):]
	if err := checkHex(alg, value); err != nil {
		return Digest{}, fmt.Errorf("invalid %s digest %q: %w", alg, s, err)
	}
	return Digest{alg: alg, value: value}, nil
}

func parseLoose(alg algorithm, s string) (Digest, error) {
	s = strings.TrimSpace(s)
	prefix := string(alg) + ":"
	value := s
	if len(s) > len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		value = s[len(prefix):]
	} else if strings.Contains(s, ":") {
		return Digest{}, fmt.Errorf("invalid %s digest %q: expected %s:<%d hex characters>", alg, s, alg, alg.hexLen())
	}
	value = strings.ToLower(value)
	if err := checkHex(alg, value); err != nil {
		return Digest{}, fmt.Errorf("invalid %s digest %q: %w", alg, s, err)
	}
	return Digest{alg: alg, value: value}, nil
}

func checkHex(alg algorithm, value string) error {
	if len(value) != alg.hexLen() {
		return fmt.Errorf("expected %d hex characters, got %d", alg.hexLen(), len(value))
	}
	for i := range value {
		c := value[i]
		if ('0' > c || c > '9') && ('a' > c || c > 'f') {
			return fmt.Errorf("contains non-lowercase-hex character %q", c)
		}
	}
	return nil
}

// put128 writes h big-endian into buf: Hi in [0:8], Lo in [8:16]. Byte order is
// frozen by the goldens of the hashutil package this replaces.
func put128(buf *[16]byte, h xxh3.Uint128) {
	binary.BigEndian.PutUint64(buf[0:8], h.Hi)
	binary.BigEndian.PutUint64(buf[8:16], h.Lo)
}

// NewSHA256 returns a streaming SHA-256 hasher for callers that hash while
// copying into several sinks and compare the sum themselves. The digest type
// itself stays the boundary: feed it the final sum via SHA256Of or compare
// with hex.EncodeToString(hasher.Sum(nil)).
func NewSHA256() hash.Hash { return sha256.New() }

// IsValid reports whether d holds a parsed digest rather than the zero value.
func (d Digest) IsValid() bool { return d.alg != "" }

// IsSHA256 reports the parse error of s as a canonical SHA-256 digest, or nil
// when it is one. It answers shape questions at boundaries that only need to
// reject malformed input, where a full parse carries no extra information.
func IsSHA256(s string) error {
	_, err := ParseSHA256(s)
	return err
}
