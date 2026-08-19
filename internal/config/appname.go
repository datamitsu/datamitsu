package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// appNamePattern is the grammar an app name must match. The constraint is a
// filesystem constraint, not a stylistic one: an app name is materialized as a
// file name (one entry per declared app in the per-root source-mode farm, and a
// directory under the content-addressed store), and it is the key `datamitsu
// exec` dispatches on. Names outside this set have escaped the store before —
// an app called "../../../.ssh" reaches filepath.Join unchecked.
//
// The 64-character ceiling keeps a name inside the 255-byte NAME_MAX every
// filesystem datamitsu targets shares, with room for the store's hash suffix.
var appNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// appNameConstraintReason is appended to every app-name error. The rule is
// otherwise arbitrary-looking, and the reader's next question is always "why is
// my name not allowed?".
const appNameConstraintReason = "app names become filesystem entries (one file per app in the source-mode farm) and command names on PATH"

// windowsReservedNames are the DOS device names. Windows refuses to create a
// file whose stem is one of these regardless of extension, so "con" and
// "con.exe" are both unusable there. website/docs promises Windows support, and
// a name that validates on macOS but cannot be written on Windows is a config
// that only fails for some of its users.
var windowsReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// validateAppName reports why name is unusable as an app name, or nil when it
// is fine. The specific cases below are all subsets of appNamePattern; they are
// enumerated first only so the message names the actual problem instead of
// printing a regular expression at someone who wrote "my tool".
func validateAppName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("app name must not be empty; %s", appNameConstraintReason)
	case name == "." || name == "..":
		return fmt.Errorf("app name %q is a directory reference; %s", name, appNameConstraintReason)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("app name %q must not contain a path separator; %s", name, appNameConstraintReason)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("app name %q must not start with a hyphen (it would parse as a command-line flag); %s", name, appNameConstraintReason)
	case windowsReservedNames[strings.ToLower(windowsNameStem(name))]:
		return fmt.Errorf("app name %q is a reserved device name on Windows; %s", name, appNameConstraintReason)
	case !appNamePattern.MatchString(name):
		return fmt.Errorf("app name %q must match %s (start with a letter or digit, then letters, digits, dots, underscores or hyphens, at most 64 characters); %s",
			name, appNamePattern, appNameConstraintReason)
	}
	return nil
}

// windowsNameStem returns the part of name before its first dot, which is what
// Windows compares against its device-name table.
func windowsNameStem(name string) string {
	stem, _, _ := strings.Cut(name, ".")
	return stem
}

// findCaseFoldCollisions reports pairs of app names that differ only in case.
// macOS and Windows filesystems are case-insensitive by default, so "Git" and
// "git" are one farm entry and one store directory: whichever is written last
// wins, silently. sortedNames must be sorted so the reported pair is stable.
func findCaseFoldCollisions(sortedNames []string) []string {
	seen := make(map[string]string, len(sortedNames))
	var errs []string
	for _, name := range sortedNames {
		folded := strings.ToLower(name)
		if first, ok := seen[folded]; ok {
			errs = append(errs, fmt.Sprintf(
				"app names %q and %q differ only in case; %s, and macOS and Windows filesystems are case-insensitive, so they would be the same file",
				first, name, appNameConstraintReason))
			continue
		}
		seen[folded] = name
	}
	return errs
}

// ValidateAppNames validates the app-name set on its own, without any of the
// per-kind checks. It exists so callers holding only a name list (the source
// farm planner) can apply the same rule doValidateApps applies.
func ValidateAppNames(names []string) error {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	var errs []string
	for _, name := range sorted {
		if err := validateAppName(name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	errs = append(errs, findCaseFoldCollisions(sorted)...)

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
