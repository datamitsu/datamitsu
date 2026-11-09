//go:build windows

package launch

import (
	"errors"
	"os"
	"os/exec"
)

func execAppCommand(path string, argv, environ []string) int {
	cmd := &exec.Cmd{Path: path, Args: argv, Env: environ, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		return 126
	}
	return 0
}
