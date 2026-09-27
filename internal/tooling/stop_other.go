//go:build !linux && !darwin && !freebsd

package tooling

// stillRunning needs no check on Windows: TerminateProcess refuses a process
// that has exited, so a stop that was delivered reached a running one.
func stillRunning(int) bool {
	return true
}
