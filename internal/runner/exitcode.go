package runner

// Exit codes beyond the usual 0/1. A tool failing and a run that did not cover
// what it was asked to cover are different outcomes, and a pipeline that treats
// them alike cannot act on either.
const (
	// ExitCoverage reports that --require-coverage was not satisfied. Distinct
	// from 1 so CI can tell "the linters found problems" from "the run did not
	// look at everything".
	ExitCoverage = 4
)

// coverageError carries ExitCoverage through the error chain.
type coverageError struct{ error }

// ExitCode implements the interface cmd checks with errors.As.
func (coverageError) ExitCode() int { return ExitCoverage }
