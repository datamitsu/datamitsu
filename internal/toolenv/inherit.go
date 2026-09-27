// Package toolenv decides the environment of a tool that fix, lint or check
// runs. datamitsu exec does not use it: it hands its app the environment it was
// started with.
package toolenv

import (
	"runtime"
	"sort"
	"strings"
)

// Inherited is an operation's inheritEnv as the host had it at one moment:
// the names asked for, and the NAME=value pairs of those the host set. Apply
// hands a tool exactly this, an absent name included, so the cache identities
// taken from Pairs describe what every process of the task was started with,
// even if the environment changes in between.
type Inherited struct {
	names []string
	pairs []string
}

// Capture resolves names against environ. A name environ does not carry yields
// no pair and one set to the empty string yields "NAME=", so the tool sees
// exactly what the host has.
func Capture(environ, names []string) Inherited {
	if len(names) == 0 {
		return Inherited{}
	}
	in := Inherited{names: names}
	for _, kv := range environ {
		name, _, found := strings.Cut(kv, "=")
		if !found || name == "" {
			continue
		}
		for _, want := range names {
			if canonical(name) == canonical(want) {
				in.pairs = append(in.pairs, kv)
				break
			}
		}
	}
	sort.Strings(in.pairs)
	return in
}

// Pairs returns the captured NAME=value pairs, sorted; nil when there are none.
func (in Inherited) Pairs() []string {
	if len(in.pairs) == 0 {
		return nil
	}
	return append([]string(nil), in.pairs...)
}

// canonical spells a variable name the way the host compares it: Windows reads
// names in any letter case.
func canonical(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
