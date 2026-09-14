// Package gittest isolates a test process from the developer's git setup.
package gittest

import (
	"os"
	"strings"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// Env returns the KEY=VALUE pairs that pin core.excludesFile to an empty file
// at command scope. Command scope outranks every config file, so a personal
// ignore file changes neither what git stages in a fixture nor what
// datamitsu's walker discovers, while the rest of the developer's config (such
// as the commit identity) stays usable.
func Env() []string {
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.excludesFile",
		"GIT_CONFIG_VALUE_0=" + os.DevNull,
	}
}

// ClearCommandScope removes command-scope git config inherited by the process
// (GIT_CONFIG_PARAMETERS and the GIT_CONFIG_COUNT family, as a hook or `git -c`
// leaves behind). It outranks every config file a test writes.
func ClearCommandScope() {
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); IsCommandScopeKey(key) {
			_ = os.Unsetenv(key)
		}
	}
}

// IsCommandScopeKey reports whether an environment variable carries
// command-scope git config.
func IsCommandScopeKey(key string) bool {
	return key == "GIT_CONFIG_PARAMETERS" || key == "GIT_CONFIG_COUNT" ||
		strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_")
}

// Isolate drops git's repository-discovery variables (see internal/gitenv),
// clears inherited command-scope config and applies Env to the process
// environment. Call it from TestMain before m.Run.
func Isolate() {
	gitenv.Unset()
	ClearCommandScope()
	for _, kv := range Env() {
		key, value, _ := strings.Cut(kv, "=")
		_ = os.Setenv(key, value)
	}
}
