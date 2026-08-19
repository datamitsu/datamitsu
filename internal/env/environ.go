package env

import (
	"os"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/ldflags"
)

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
		if strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	sort.Strings(out)
	return out
}
