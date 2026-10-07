package config

import (
	"fmt"
	"strings"
)

// Severity names a level of the core's diagnostic scale in configuration.
type Severity string

// The levels of the scale, most serious first.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
	SeverityHint    Severity = "hint"
)

// DefaultFailOn is the threshold of an operation that sets no failOn: a
// finding fails a run only at error level.
const DefaultFailOn = SeverityError

// severityNames lists the valid values in scale order, for messages.
var severityNames = []Severity{SeverityError, SeverityWarning, SeverityInfo, SeverityHint}

// Level is the value of s on the core's 1–4 scale (1 error … 4 hint), the
// numbering diagnostic.Severity uses; 0 for a value that is not a level. It
// returns a plain number because internal/diagnostic depends on this package
// through internal/parsermanager.
func (s Severity) Level() uint8 {
	for i, name := range severityNames {
		if s == name {
			return uint8(i + 1)
		}
	}
	return 0
}

// Valid reports whether s names a level.
func (s Severity) Valid() bool {
	return s.Level() != 0
}

// ParseSeverity reads a level name as a flag or an environment variable gives
// it, and says what it expected when the value is not one.
func ParseSeverity(raw string) (Severity, error) {
	s := Severity(raw)
	if !s.Valid() {
		return "", fmt.Errorf("%q is not a level (must be %s)", raw, SeverityChoices())
	}
	return s, nil
}

// SeverityChoices lists the level names for a message: "error, warning, info
// or hint".
func SeverityChoices() string {
	names := make([]string, len(severityNames))
	for i, name := range severityNames {
		names[i] = string(name)
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// EffectiveFailOn is the threshold an operation runs with: the stricter of its
// own failOn (DefaultFailOn when unset) and global, a raise for every
// operation of the run that can never lower one. Stricter means a later level
// of the scale, which fails on more findings.
func EffectiveFailOn(op ToolOperation, global Severity) Severity {
	own := op.FailOn
	if !own.Valid() {
		own = DefaultFailOn
	}
	if global.Valid() && global.Level() > own.Level() {
		return global
	}
	return own
}

func failOnErrors(toolName, opType string, failOn Severity) []string {
	if failOn == "" || failOn.Valid() {
		return nil
	}
	return []string{fmt.Sprintf("tool %q operation %q: failOn %q is not a level (must be %s)",
		toolName, opType, failOn, SeverityChoices())}
}
