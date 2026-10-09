package report

import (
	"bytes"
	"encoding/hex"
	"sort"
	"strconv"

	"github.com/datamitsu/datamitsu/internal/digest"
)

// A finding's fingerprint is SHA-256, not XXH3, because it leaves the process:
// code scanning stores it and compares it with the next upload's, so it is an
// identifier in another system's database rather than an internal key.
//
//	input    = "dmfp1" NUL tool NUL code NUL relPath NUL lineHash NUL ordinal
//	lineHash = hex(sha256(start line without trailing whitespace))
//	ordinal  = 0-based position among the findings with the same tool, code,
//	           relPath and lineHash, ordered by row, column, source, message
//
// The message is not an input, so rewording one does not turn every alert
// into "fixed" and "new"; the tool comes first, so two tools never share one.

// FingerprintVersion names the fingerprint input, whose first part it is: a
// baseline and a diff compare fingerprints of one version only.
const FingerprintVersion = "dmfp1"

// Fingerprint bases: what a fingerprint rests on.
const (
	// BasisLine: the text of the finding's start line.
	BasisLine = "line"
	// BasisRow: the row number, for a file that could not be read.
	BasisRow = "row"
	// BasisNone: nothing, for a finding that names no file.
	BasisNone = "none"
)

// LineHash is the line component of a fingerprint: the hex SHA-256 of line
// without its trailing whitespace.
func LineHash(line []byte) string {
	return digest.SHA256Of(bytes.TrimRight(line, " \t\r\v\f")).Hex()
}

// RowHash stands in for LineHash when the line cannot be read.
func RowHash(row int) string {
	return "row:" + strconv.Itoa(row)
}

// Fingerprint is the 64-character lowercase hex identity of a finding.
func Fingerprint(tool, code, relPath, lineHash string, ordinal int) string {
	h := digest.NewSHA256()
	for i, part := range []string{FingerprintVersion, tool, code, relPath, lineHash, strconv.Itoa(ordinal)} {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fingerprintInput is what the fingerprints of a set of findings are computed
// from.
type fingerprintInput struct {
	tool, code, relPath, lineHash string
	row, col                      int
	source, message               string
}

// ordinals numbers each input among those with its tool, code, path and line
// hash, by row, column, source and message; inputs equal in all of those keep
// their order.
func ordinals(in []fingerprintInput) []int {
	order := make([]int, len(in))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := in[order[i]], in[order[j]]
		switch {
		case a.tool != b.tool:
			return a.tool < b.tool
		case a.code != b.code:
			return a.code < b.code
		case a.relPath != b.relPath:
			return a.relPath < b.relPath
		case a.lineHash != b.lineHash:
			return a.lineHash < b.lineHash
		case a.row != b.row:
			return a.row < b.row
		case a.col != b.col:
			return a.col < b.col
		case a.source != b.source:
			return a.source < b.source
		default:
			return a.message < b.message
		}
	})
	out := make([]int, len(in))
	n := 0
	for k, idx := range order {
		if k > 0 {
			prev := in[order[k-1]]
			cur := in[idx]
			if prev.tool != cur.tool || prev.code != cur.code || prev.relPath != cur.relPath || prev.lineHash != cur.lineHash {
				n = 0
			}
		}
		out[idx] = n
		n++
	}
	return out
}

func fingerprints(in []fingerprintInput) []string {
	numbers := ordinals(in)
	out := make([]string, len(in))
	for i, f := range in {
		out[i] = Fingerprint(f.tool, f.code, f.relPath, f.lineHash, numbers[i])
	}
	return out
}
