// Package toolenv decides the environment of a tool that fix, lint or check
// runs. datamitsu exec does not use it: it hands its app the environment it was
// started with.
package toolenv

import (
	"runtime"
	"sort"
	"strings"
)

// Resolve returns environ's entries for names, sorted: the host values an
// operation's inheritEnv hands its tool. A name environ does not carry yields
// nothing and one set to the empty string yields "NAME=", so the tool sees
// exactly what the host has.
func Resolve(environ, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	var out []string
	for _, kv := range environ {
		name, _, found := strings.Cut(kv, "=")
		if !found || name == "" {
			continue
		}
		for _, want := range names {
			if canonical(name) == canonical(want) {
				out = append(out, kv)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// canonical spells a variable name the way the host compares it: Windows reads
// names in any letter case.
func canonical(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
