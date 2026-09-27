// Package exitcode holds the process exit codes datamitsu uses beyond 0 and 1,
// and the error types that carry them to the single exit site in cmd. It is a
// leaf package, so cmd and internal/runner agree on the codes without importing
// each other.
//
// A pipeline can then tell "you called it wrong" (Usage) from "the code is
// bad" (1) from "we did not look at everything" (Coverage).
package exitcode

import "fmt"

const (
	// Usage is the code of a caller mistake: wrong arguments, incompatible
	// flags, a refusal known before anything runs.
	Usage = 2
	// Coverage is the code of a run that did not cover what it was asked to
	// (--require-coverage).
	Coverage = 4
)

// UsageError is a caller mistake. It exits Usage with the wrapped message.
type UsageError struct{ Err error }

func (e UsageError) Error() string { return e.Err.Error() }

func (e UsageError) Unwrap() error { return e.Err }

// ExitCode implements the interface the exit site checks with errors.As.
func (UsageError) ExitCode() int { return Usage }

// UsageErrorf returns a UsageError with a formatted message; %w wraps as in fmt.Errorf.
func UsageErrorf(format string, args ...any) error {
	return UsageError{Err: fmt.Errorf(format, args...)}
}

// CoverageError reports a run that did not answer completely. It exits
// Coverage with the wrapped message.
type CoverageError struct{ Err error }

func (e CoverageError) Error() string { return e.Err.Error() }

func (e CoverageError) Unwrap() error { return e.Err }

// ExitCode implements the interface the exit site checks with errors.As.
func (CoverageError) ExitCode() int { return Coverage }

// CoverageErrorf returns a CoverageError with a formatted message.
func CoverageErrorf(format string, args ...any) error {
	return CoverageError{Err: fmt.Errorf(format, args...)}
}
