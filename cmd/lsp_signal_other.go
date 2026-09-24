//go:build !unix

package cmd

// absorbBrokenPipe has nothing to do without SIGPIPE: a write to a closed pipe
// only returns an error.
func absorbBrokenPipe() {}
