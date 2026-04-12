// Package color provides color utilities for terminal output.
//
// Color detection strategy (in order):
//  1. NO_COLOR env var set by user -> disable all colors
//  2. FORCE_COLOR env var set by user -> enable colors
//  3. CLICOLOR_FORCE env var set by user -> enable colors
//  4. CLICOLOR=0 env var set by user -> disable colors
//  5. Fall back to terminal capability detection (is stdout a TTY?)
//
// For child process color preservation, we use env hints (FORCE_COLOR=1,
// CLICOLOR_FORCE=1) rather than PTY or streaming approaches. This preserves
// the single-print-layer architecture where the executor captures output
// and the runner prints it once. Child processes detect a pipe (not TTY)
// since their stdout goes to a buffer, so env hints tell them to emit
// ANSI sequences anyway. The runner passes through these sequences when
// printing to the actual terminal.
package color

import (
	"os"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
)

// Enabled returns true if color output should be used.
// Checks user env vars first, then falls back to terminal detection.
func Enabled() bool {
	// NO_COLOR (https://no-color.org/) - if set (any value), disable colors
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}

	// FORCE_COLOR - if set (any non-empty value), enable colors
	if v, ok := os.LookupEnv("FORCE_COLOR"); ok && v != "" && v != "0" {
		return true
	}

	// CLICOLOR_FORCE - if set to non-zero, enable colors even without TTY
	if v, ok := os.LookupEnv("CLICOLOR_FORCE"); ok && v != "" && v != "0" {
		return true
	}

	// CLICOLOR=0 - explicitly disable
	if v, ok := os.LookupEnv("CLICOLOR"); ok && v == "0" {
		return false
	}

	// Fall back to terminal detection
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// Init configures the fatih/color library based on environment detection.
// Call this once at startup.
func Init() {
	if !Enabled() {
		color.NoColor = true
	} else {
		color.NoColor = false
	}
}

// ChildEnvHints returns environment variables to set on child processes
// to preserve color output. Only returns hints when color is enabled
// and the user hasn't explicitly set these variables.
func ChildEnvHints() map[string]string {
	if !Enabled() {
		return nil
	}

	hints := make(map[string]string)

	// Only set FORCE_COLOR if user hasn't already set it
	if _, ok := os.LookupEnv("FORCE_COLOR"); !ok {
		hints["FORCE_COLOR"] = "1"
	}

	// Only set CLICOLOR_FORCE if user hasn't already set it
	if _, ok := os.LookupEnv("CLICOLOR_FORCE"); !ok {
		hints["CLICOLOR_FORCE"] = "1"
	}

	return hints
}

// Convenience color functions for runner service output

var (
	Red    = color.New(color.FgRed).SprintFunc()
	Green  = color.New(color.FgGreen).SprintFunc()
	Yellow = color.New(color.FgYellow).SprintFunc()
	Cyan   = color.New(color.FgCyan).SprintFunc()
	Bold   = color.New(color.Bold).SprintFunc()
	Faint  = color.New(color.Faint).SprintFunc()
)
