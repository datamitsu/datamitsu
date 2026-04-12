package hashutil

import (
	"encoding/binary"
	"encoding/hex"
	"io"

	"github.com/zeebo/xxh3"
)

// uint128ToBytes converts an xxh3.Uint128 to a big-endian 16-byte array.
func uint128ToBytes(h xxh3.Uint128) [16]byte {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], h.Hi)
	binary.BigEndian.PutUint64(buf[8:16], h.Lo)
	return buf
}

// XXH3Hex returns the XXH3-128 hash of data as a lowercase 32-character hex string.
func XXH3Hex(data []byte) string {
	buf := uint128ToBytes(xxh3.Hash128(data))
	return hex.EncodeToString(buf[:])
}

// XXH3Multi concatenates parts with \0 separator and returns the XXH3-128 hash
// as a lowercase 32-character hex string.
func XXH3Multi(parts ...[]byte) string {
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
	return XXH3Hex(buf)
}

// XXH3Reader reads all data from r and returns the XXH3-128 hash as a
// lowercase 32-character hex string. Uses streaming to avoid loading the
// entire content into memory.
func XXH3Reader(r io.Reader) (string, error) {
	h := xxh3.New128()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	buf := uint128ToBytes(h.Sum128())
	return hex.EncodeToString(buf[:]), nil
}
