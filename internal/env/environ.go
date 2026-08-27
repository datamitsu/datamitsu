package env

import (
	"os"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/ldflags"
)

// observationExcluded lists the variables that must not enter ANY fingerprint,
// because they change what datamitsu *reports about itself* and never what it
// produces. Folding tracing in would mean turning it on invalidates every baked
// farm, so the first traced invocation would measure a rebake instead of the
// command under study — the instrument would change the measurement. The
// config-cache switch is on the same footing: it decides whether an
// already-computed evaluation is consulted, so folding it into the config-eval
// key would mean a run with the cache off wrote an entry no run with it on
// could ever read.
var observationExcluded = map[string]bool{
	trace.Name:       true,
	traceDir.Name:    true,
	configCache.Name: true,
}

// environExcluded is the source-mode staleness key's exclusion list: the
// observation-only variables, plus the activation markers.
//
// The activation markers describe *which* farm a shell activated rather than
// *how* datamitsu behaves. Including them would make the source-mode staleness
// key disagree with itself across shells: a farm is baked by a process that does
// not yet have them set, then every command in the activated shell runs with
// them set, reports the manifest stale, and rebakes. Two activated repositories
// would rebake each other's farm on every `cd`.
//
// They are deliberately NOT excluded from EnvironAll: config JS reads the whole
// unfiltered environment through facts().env, so a config branching on an
// activation marker must get a different config-eval key. There is no rebake
// loop on that side to protect against — an extra entry is the whole cost.
var environExcluded = func() map[string]bool {
	m := map[string]bool{
		sourceRoot.Name:       true,
		sourceFarm.Name:       true,
		sourceFarmConfig.Name: true,
	}
	for name := range observationExcluded {
		m[name] = true
	}
	return m
}()

// ObservationOnly reports whether a variable is observation-only, i.e. excluded
// from every fingerprint by observationExcluded.
//
// It exists so the environment config JS can read can be filtered by the same
// list EnvironAll hashes by. A variable dropped from the fingerprint but left
// visible to config JS is a config input that cannot move the config-eval key.
func ObservationOnly(name string) bool {
	return observationExcluded[name]
}

// EnvironAll returns the ENTIRE environment as sorted "NAME=VALUE" strings,
// minus the observation-only variables of observationExcluded.
//
// It is the environment fingerprint for anything config JS can read, which is
// all of it through facts().env: the shared config branches on CI, and a
// DATAMITSU_*-only fingerprint (Environ) is defeated by that one variable.
// Only the observation-only exclusions carry over — anything else config JS can
// read has to be able to move the key, or a cache hit is not a function of its
// inputs.
func EnvironAll() []string {
	return canonicalEnviron(os.Environ(), func(name string) bool {
		return observationExcluded[name]
	})
}

// canonicalEnviron reduces raw "NAME=VALUE" entries to one entry per name,
// sorted, dropping the names exclude reports.
//
// The environment a process inherits may repeat a name — nothing rejects
// `FOO=1 FOO=2` — and os.Environ() reports every occurrence verbatim. Sorting
// the raw entries would hash `FOO=1, FOO=2` and `FOO=2, FOO=1` identically
// while facts.collectAllEnv, which builds a map, resolves them to different
// values. Last occurrence wins here for the same reason it wins there: a
// fingerprint that disagrees with what config JS reads is a wrong cache hit.
func canonicalEnviron(raw []string, exclude func(string) bool) []string {
	byName := make(map[string]string, len(raw))
	for _, kv := range raw {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || exclude(name) {
			continue
		}
		byName[name] = kv
	}

	out := make([]string, 0, len(byName))
	for _, kv := range byName {
		out = append(out, kv)
	}
	sort.Strings(out)
	return out
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

	return canonicalEnviron(os.Environ(), func(name string) bool {
		return !strings.HasPrefix(name, prefix) || environExcluded[name]
	})
}
