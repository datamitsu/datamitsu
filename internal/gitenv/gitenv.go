// Package gitenv keeps child git processes anchored to the directory they are
// pointed at, instead of a repository inherited from the environment.
//
// Git exports GIT_DIR and friends to every hook it runs, and those variables
// take precedence over the working-directory search. A git command that sets an
// explicit Dir therefore operates on the hook's repository rather than on the
// directory it was given. datamitsu runs from git hooks (see lefthook.yaml),
// and the pre-commit hook runs the whole Go test suite, which is enough for
// tests to read - and write - the real checkout instead of their temporary
// fixtures.
//
// Every call site in this repository passes git an explicit directory and means
// it, so the rule here is uniform: the repository, and the index within it, are
// the ones belonging to that directory.
package gitenv

import (
	"os"
	"slices"
	"strings"
)

// hookVars are the variables git exports to hooks that bind a child process to
// the hook's repository. They are removed as a set: dropping only some of them
// leaves git with a mismatched pair - a repository found by directory search
// combined with an inherited index - which git rejects outright (exit 128).
//
// Removing GIT_INDEX_FILE costs one nuance: during a partial commit
// (git commit -- path) git stages into a temporary index, so staged-file
// discovery reads the repository index and reports a superset of that commit's
// files. Over-reporting files to lint is a far milder failure than operating on
// the wrong repository.
var hookVars = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_CEILING_DIRECTORIES",
	"GIT_COMMON_DIR",
	"GIT_DIR",
	"GIT_INDEX_FILE",
	"GIT_NAMESPACE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_PREFIX",
	"GIT_WORK_TREE",
}

// Environ returns the current environment without git's hook variables, ready
// to assign to exec.Cmd.Env.
func Environ() []string {
	return Sanitize(os.Environ())
}

// Sanitize returns env without git's hook variables. Entries that carry no "="
// are passed through untouched.
func Sanitize(env []string) []string {
	out := make([]string, 0, len(env))

	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && slices.Contains(hookVars, name) {
			continue
		}
		out = append(out, entry)
	}

	return out
}

// Unset removes git's hook variables from the current process. TestMain calls
// this so that tests invoking git directly are hermetic too: without it a
// test's own "git init", "git add" or "git commit" lands in whichever
// repository the surrounding hook was running for.
func Unset() {
	for _, name := range hookVars {
		_ = os.Unsetenv(name)
	}
}
