//go:build windows

package shim

import (
	"errors"
	"os"
	"os/exec"
)

// execProcess runs the target as a child and exits with its status.
//
// Windows has no execve, so the one-process guarantee the Unix path gives is not
// available: this waits for the child and adopts its exit code. Signals and job
// control are the operating system's to arrange.
func execProcess(path string, argv, environ []string) error {
	// #nosec G204 -- path comes from the farm manifest, which datamitsu wrote.
	cmd := exec.Command(path, argv[1:]...)
	cmd.Env = environ
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
