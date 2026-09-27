//go:build !linux

package tooling

// stillRunning cannot ask whether a child has exited without reaping it here,
// so a delivered stop counts.
func stillRunning(int) bool {
	return true
}
