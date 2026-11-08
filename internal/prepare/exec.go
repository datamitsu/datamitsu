package prepare

// processAlive reports whether a process with the given PID exists.
func processAlive(pid int) bool {
	return processProbe(pid) == nil
}
