package cmd

// CodedError is an error that carries its own process exit code. Errors that do
// not implement it exit 1, so nothing that existed before this changes.
type CodedError interface {
	error
	ExitCode() int
}
