package tooling

import (
	"time"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/trace"
)

// cntProbeRehash counts the files the post-run probe had to read again because
// a stat could not prove them unchanged; every other file costs one stat.
var cntProbeRehash = trace.NewCounter("cache.file_probe_rehashes")

// observe reads what a tool is about to be handed: the hash of path's bytes and
// the stat of the handle they were read through, through the process-wide
// content memo. A path that cannot be read yields a Seen without a hash, which
// the cache treats as a miss and never records.
func observe(path string) cache.Seen {
	at := time.Now()
	st, _ := contentHash(path, contentMemo, memoShared)
	if !st.read {
		return cache.Seen{At: at}
	}
	return cache.Seen{Hash: st.hash, Size: st.size, ModTime: st.mod, Identity: st.ident, At: at}
}

// unchangedSince reports whether path still holds the bytes seen describes,
// with the probe the verdict cache uses after a read-only run: a stat settles
// it when it can, and anything a stat cannot rule out — a moved size, time or
// identity, a platform without a change time, a write inside the mtime tick —
// is read again. The re-read bypasses the memo, which the pre-run observation
// filled, and replaces what it held.
func unchangedSince(path string, seen cache.Seen) bool {
	if seen.Hash == "" {
		return false
	}
	st := pathState{path: path, hash: seen.Hash, size: seen.Size, mod: seen.ModTime, ident: seen.Identity, read: true}
	if statSettled(st, seen.At) {
		return true
	}
	cntProbeRehash.Add(1)
	fresh, _ := contentHash(path, contentMemo, memoRewrite)
	return fresh.read && fresh.hash == seen.Hash
}
