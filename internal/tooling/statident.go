package tooling

import "os"

// fileIdent is the part of a file's identity that a writer cannot put back the
// way it found it. Size and modification time can both be restored — `rsync -a`,
// `cp -p`, `tar -x` and anything else that calls `utimes`/`Chtimes` do exactly
// that — so a rewrite of the same length with the original mtime restored is
// invisible to a (size, mtime) comparison no matter how the mtime tick guard is
// anchored. The inode-change time is not restorable: the write bumps it, and the
// call that puts the mtime back bumps it again.
//
// known says the platform reported a change time at all. It is not a detail:
// without it the zero value would compare equal to itself and a platform with no
// change time would silently fall back to trusting (size, mtime) — the very
// comparison the restored-mtime rewrite defeats. Every caller therefore treats
// an unknown identity as "this stat proves nothing", which costs a re-read of
// the bytes and never a wrong skip. Windows is that platform: no change time is
// reachable from a path-only stat, so the memo never hits there and the post-run
// probe re-hashes, exactly as both did before either existed.
//
// Only ino and ctime are recorded. The device is deliberately left out: it needs
// a signed-to-unsigned conversion whose width differs per platform, and a path
// that changes device mid-run without changing its inode-change time is not a
// case worth the conversion.
type fileIdent struct {
	ino       uint64
	ctimeNano int64
	known     bool
}

// identOf reads the non-restorable part of a stat. It never fails: an
// unrecognised Sys() yields the zero value, whose known flag is false, and every
// comparison against it is a miss.
func identOf(fi os.FileInfo) fileIdent {
	if fi == nil {
		return fileIdent{}
	}
	return statIdent(fi)
}
