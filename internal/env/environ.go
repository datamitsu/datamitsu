package env

import (
	"os"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// environExcluded lists datamitsu-owned variables that describe *which* farm a
// shell activated rather than *how* datamitsu behaves, and so must not enter a
// fingerprint.
//
// Including them would make the source-mode staleness key disagree with itself
// across shells: a farm is baked by a process that does not yet have them set,
// then every command in the activated shell runs with them set, reports the
// manifest stale, and rebakes. Two activated repositories would rebake each
// other's farm on every `cd`.
var environExcluded = map[string]bool{
	sourceRoot.Name: true,
	sourceFarm.Name: true,
}

// Environ returns every datamitsu-owned environment variable currently set, as
// sorted "NAME=VALUE" strings.
//
// It exists so callers that need a fingerprint of datamitsu's configuration —
// the source-mode farm staleness key is the first — can obtain one without
// reaching for os.Getenv themselves, and without enumerating the individual
// getters (which would silently drift as new variables are added).
//
// The result is deliberately a superset of the variables that feed
// runtimeconfig: over-invalidating a cache is correct but slow, while missing a
// variable is a stale result. Ordering is stable so the value can be hashed.
func Environ() []string {
	prefix := strings.ToUpper(ldflags.PackageName) + "_"

	all := os.Environ()
	out := make([]string, 0, 8)
	for _, kv := range all {
		if !strings.HasPrefix(kv, prefix) {
			continue
		}
		if name, _, ok := strings.Cut(kv, "="); ok && environExcluded[name] {
			continue
		}
		out = append(out, kv)
	}
	sort.Strings(out)
	return out
}
