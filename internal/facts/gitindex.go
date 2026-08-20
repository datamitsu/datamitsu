package facts

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
)

// maxGitIndexSize bounds how much of a repository index is read when proving a
// submodule registration. A repository whose index is larger than this is not
// one to scan on the startup path — the walk declines and git answers.
const maxGitIndexSize = 16 << 20

// gitIndexSignature is the four-byte magic every index file starts with.
const gitIndexSignature = "DIRC"

const (
	// gitIndexHeaderLen is the signature, the version and the entry count.
	gitIndexHeaderLen = 12
	// gitIndexStatLen is the fixed stat data every entry begins with; the mode
	// sits at offset 24 within it.
	gitIndexStatLen  = 40
	gitIndexModeOff  = 24
	gitIndexNameMask = 0x0FFF
	// gitIndexStageMask selects the merge stage of an entry. A clean index holds
	// stage 0 only; a conflict replaces that with stages 1, 2 and 3.
	gitIndexStageMask  = 0x3000
	gitIndexStageShift = 12
	// gitIndexExtendedFlag marks an entry carrying a second flags word, which
	// only index version 3 and later may write.
	gitIndexExtendedFlag = 0x4000
	// A gitlink — the mode 160000 entry that makes a directory a submodule.
	gitIndexObjectTypeMask = 0xF000
	gitIndexGitlinkType    = 0xE000
)

// gitIndexObjectIDLengths are the digest sizes an index may use, SHA-1 first.
// The index header does not record which one is in play, so the scan tries both
// and keeps the one whose entries and extensions account for every byte up to
// the trailing checksum (see scanGitIndex).
var gitIndexObjectIDLengths = []int{20, 32}

// recordsGitlinkAt reports whether the repository whose working tree root is
// super records a gitlink at rel — the mode 160000 index entry that is exactly
// what `git rev-parse --show-superproject-working-tree` looks for before it
// calls a directory a submodule of super.
//
// It is a proof, not a heuristic: anything it cannot read and account for byte
// for byte — a missing or oversized index, index version 4 (whose entry paths
// are prefix-compressed against their predecessor), an entry shape it does not
// recognise, a digest size neither 20 nor 32 bytes, trailing bytes that do not
// parse as extensions, a path recorded only at a nonzero merge stage — reports
// false, and the caller declines the climb and
// lets git answer. A false negative costs one subprocess; a false positive
// would silently produce the wrong project root.
func recordsGitlinkAt(super, rel string) bool {
	gitdir, kind := classifyGitLink(super)
	if kind == gitLinkUnknown {
		return false
	}

	path := filepath.Join(gitdir, "index")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxGitIndexSize {
		return false
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- path is <gitdir>/index, size-checked above
	if err != nil {
		return false
	}

	for _, idLen := range gitIndexObjectIDLengths {
		if found, ok := scanGitIndex(raw, idLen, rel); ok {
			return found
		}
	}
	return false
}

// scanGitIndex reads raw as an index whose object ids are idLen bytes and
// reports whether it holds a gitlink at want. The second return value is
// whether the bytes parsed as such an index at all — false means "these are not
// index-with-this-digest-size bytes", which is how the digest size is settled.
func scanGitIndex(raw []byte, idLen int, want string) (found, ok bool) {
	if len(raw) < gitIndexHeaderLen+idLen || string(raw[:4]) != gitIndexSignature {
		return false, false
	}

	// Version 4 prefix-compresses each entry path against the previous one, so
	// a path cannot be read without reconstructing every path before it. Not a
	// format to reimplement on the startup path.
	version := binary.BigEndian.Uint32(raw[4:8])
	if version != 2 && version != 3 {
		return false, false
	}

	pos := gitIndexHeaderLen
	for range binary.BigEndian.Uint32(raw[8:12]) {
		e, entryOK := readGitIndexEntry(raw, pos, idLen, version)
		if !entryOK {
			return false, false
		}
		// Stage 0 only. Git decides this by reading the first `ls-files --stage`
		// line for the path, and index entries sharing a path are ordered by
		// stage, so a conflicted path — which has no stage 0 entry at all — is
		// answered by whatever its lowest stage happens to hold. Rather than
		// reproduce that, a path in conflict simply fails to match and the climb
		// is declined; one subprocess is the price of not guessing.
		if e.name == want && e.stage == 0 && e.mode&gitIndexObjectTypeMask == gitIndexGitlinkType {
			found = true
		}
		pos = e.next
	}

	if !gitIndexEndsCleanly(raw, pos, idLen) {
		return false, false
	}
	return found, true
}

// gitIndexEntry is one decoded index entry: what it records, where, at which
// merge stage, and where the entry after it starts.
type gitIndexEntry struct {
	mode  uint32
	name  string
	stage uint16
	next  int
}

// readGitIndexEntry decodes the entry starting at pos.
func readGitIndexEntry(raw []byte, pos, idLen int, version uint32) (entry gitIndexEntry, ok bool) {
	// Fixed stat data, the object id, then the 16-bit flags word.
	head := pos + gitIndexStatLen + idLen + 2
	if head > len(raw) {
		return gitIndexEntry{}, false
	}
	mode := binary.BigEndian.Uint32(raw[pos+gitIndexModeOff : pos+gitIndexModeOff+4])
	flags := binary.BigEndian.Uint16(raw[pos+gitIndexStatLen+idLen : head])

	if flags&gitIndexExtendedFlag != 0 {
		if version < 3 {
			return gitIndexEntry{}, false
		}
		head += 2
		if head > len(raw) {
			return gitIndexEntry{}, false
		}
	}

	// A path of 0xFFF bytes or longer stores that value as its length and is
	// NUL-terminated instead.
	var end int
	if nameLen := int(flags & gitIndexNameMask); nameLen < gitIndexNameMask {
		end = head + nameLen
		if end > len(raw) {
			return gitIndexEntry{}, false
		}
	} else {
		i := bytes.IndexByte(raw[head:], 0)
		if i < 0 {
			return gitIndexEntry{}, false
		}
		end = head + i
	}

	// One to eight NUL bytes pad every entry to a multiple of eight bytes.
	next := pos + (end-pos+8)&^7
	if next > len(raw) {
		return gitIndexEntry{}, false
	}
	if !allZero(raw[end:next]) {
		return gitIndexEntry{}, false
	}

	return gitIndexEntry{
		mode:  mode,
		name:  string(raw[head:end]),
		stage: (flags & gitIndexStageMask) >> gitIndexStageShift,
		next:  next,
	}, true
}

// gitIndexEndsCleanly reports whether everything from pos to the trailing
// checksum parses as the sequence of sized extension blocks git writes there.
// This is what makes the digest-size guess safe: a wrong guess leaves the entry
// walk misaligned, and misaligned bytes do not account for the file exactly.
func gitIndexEndsCleanly(raw []byte, pos, idLen int) bool {
	for pos+idLen < len(raw) {
		// A four-byte signature and a four-byte size, then the block itself.
		if pos+8 > len(raw) {
			return false
		}
		size := int(binary.BigEndian.Uint32(raw[pos+4 : pos+8]))
		if size > len(raw)-pos-8 {
			return false
		}
		pos += 8 + size
	}
	return pos+idLen == len(raw)
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
