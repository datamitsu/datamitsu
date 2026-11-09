//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

func executeRuntimeRequest(request runtimeExecRequest) {
	argv := append([]string{request.Executable}, request.Args...)
	environ := os.Environ()
	for key, value := range request.Env {
		environ = replaceEnv(environ, key, value)
	}
	// request.Executable is resolved from a validated named runtime; argv is forwarded
	// unchanged as discrete arguments, never interpreted by a shell.
	if err := syscall.Exec(request.Executable, argv, environ); err != nil { //nolint:gosec // G204: trusted resolved interpreter; user args remain direct argv by contract
		_, _ = os.Stderr.WriteString("error: start runtime: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func replaceEnv(values []string, key, value string) []string {
	prefix := key + "="
	for i, current := range values {
		if len(current) >= len(prefix) && current[:len(prefix)] == prefix {
			values[i] = prefix + value
			return values
		}
	}
	return append(values, prefix+value)
}
