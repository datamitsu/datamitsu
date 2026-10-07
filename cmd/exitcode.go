package cmd

// CodedError is an error that carries its own process exit code; the codes live
// in internal/exitcode. Errors that do not implement it exit 1.
type CodedError interface {
	error
	ExitCode() int
}
