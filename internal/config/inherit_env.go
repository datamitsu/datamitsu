package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// inheritEnvErrors checks an operation's inheritEnv. A name nothing strips is
// accepted: it changes nothing today and keeps working if the stripped list
// grows. PATH is refused because an inherited host PATH would compete with the
// one the runtime owns, and DATAMITSU_* because datamitsu's own variables are
// read through internal/env, never handed to a tool.
func inheritEnvErrors(toolName, opType string, names []string) []string {
	var errs []string
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		field := fmt.Sprintf("tool %q operation %q: inheritEnv %q", toolName, opType, name)
		switch {
		case !envNameRE.MatchString(name):
			errs = append(errs, field+": not a variable name (want uppercase letters, digits and _, not starting with a digit)")
		case seen[name]:
			errs = append(errs, field+": listed more than once")
		case name == "NO_COLOR":
			errs = append(errs, field+": NO_COLOR is reserved for datamitsu; it cannot be inherited")
		case name == "PATH":
			errs = append(errs, field+": PATH cannot be inherited, it would replace the PATH the runtime sets; list the binary in the app's dependsOn to put it on PATH")
		case strings.HasPrefix(name, "DATAMITSU_"):
			errs = append(errs, field+": datamitsu's own variables are not handed to tools")
		}
		seen[name] = true
	}
	return errs
}

// noColorEnvErrors refuses NO_COLOR in an operation's env, in any letter case
// because Windows reads the variable in any case.
func noColorEnvErrors(toolName, opType string, env map[string]string) []string {
	var errs []string
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if strings.EqualFold(key, "NO_COLOR") {
			errs = append(errs, fmt.Sprintf(
				"tool %q operation %q: env %q: NO_COLOR is reserved for datamitsu; it cannot be set",
				toolName, opType, key,
			))
		}
	}
	return errs
}
