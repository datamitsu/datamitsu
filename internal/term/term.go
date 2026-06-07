// Package term centralizes detection of the output environment (TTY vs CI vs
// plain pipe) so every datamitsu code path makes a consistent rendering
// decision. It is the single source of truth that replaces the scattered
// env.IsCI()/isatty checks previously made independently by the color, runner
// and binmanager packages.
package term

import (
	"os"

	"github.com/datamitsu/datamitsu/internal/env"

	"github.com/mattn/go-isatty"
)

// Mode describes how output should be rendered.
type Mode int

const (
	// Interactive means stdout is an animated terminal — progress bars, cursor
	// movement and live redraws are allowed.
	Interactive Mode = iota
	// Plain means CI or a non-TTY pipe — no ANSI cursor control, no
	// carriage-return redraws. Progress is surfaced as throttled, append-only
	// lines.
	Plain
)

// String renders the mode name (handy for tests and debug output).
func (m Mode) String() string {
	if m == Interactive {
		return "interactive"
	}
	return "plain"
}

// IsCI reports whether a CI environment is detected. It delegates to the env
// package so all environment access stays centralized there.
func IsCI() bool {
	return env.IsCI()
}

// IsTTY reports whether stdout is an interactive terminal.
func IsTTY() bool {
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// DetectMode resolves the effective output mode: Plain under CI or when stdout
// is not a terminal, Interactive otherwise.
func DetectMode() Mode {
	if IsCI() || !IsTTY() {
		return Plain
	}
	return Interactive
}
