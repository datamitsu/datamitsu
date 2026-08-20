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
	// Built by hand rather than through exec.Command, which would overwrite
	// Args[0] with path. The caller computed argv[0] as the name the user typed,
	// and dropping it here would make the same command print a content-addressed
	// store path as its program name on Windows and the plain name everywhere
	// else.
	// #nosec G204 -- path comes from the farm manifest, which datamitsu wrote.
	cmd := &exec.Cmd{
		Path:   path,
		Args:   argv,
		Env:    environ,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

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
