package lsp

import (
	"errors"
	"io/fs"
	"os"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// Markers for a watched file with no content to hash. Neither is a valid
// XXH3-128 hex digest, so they never collide with one.
const (
	digestAbsent     = "absent"
	digestUnreadable = "unreadable"
)

// inputDigests is the content digest of each file a configuration was loaded
// from, by path. Content rather than mtime: it follows a symlinked wrapper in
// node_modules that a version bump points elsewhere, and has no
// mtime-granularity holes; hashing the whole chain costs well under a
// millisecond.
type inputDigests map[string]string

// digestInputs reads every path in paths. A missing file is recorded too: a
// config that appears is as much a change as one that is edited.
func digestInputs(paths []string) inputDigests {
	digests := make(inputDigests, len(paths))
	for _, path := range paths {
		if _, seen := digests[path]; !seen {
			digests[path] = digestFile(path)
		}
	}
	return digests
}

// fingerprint is one XXH3-128 over every path and its digest, in path order.
func (d inputDigests) fingerprint() string {
	parts := make([][]byte, 0, 2*len(d))
	for _, path := range sortedKeys(d) {
		parts = append(parts, []byte(path), []byte(d[path]))
	}
	return hashutil.XXH3Multi(parts...)
}

func digestFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return digestAbsent
		}
		return digestUnreadable
	}
	defer func() { _ = f.Close() }()
	sum, err := hashutil.XXH3Reader(f)
	if err != nil {
		return digestUnreadable
	}
	return sum
}
