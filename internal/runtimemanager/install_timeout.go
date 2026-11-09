package runtimemanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/installprocess"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// install_timeout.go threads the per-app install timeout (the effective
// InstallTimeoutSeconds from runtimeconfig) through the runtime-managed app
// installs the way binmanager does for binary downloads. The runtime layer
// additionally spawns subprocesses (uv sync, pnpm install, go build), so it
// carries both an HTTP-download path and a subprocess path off the same deadline.

func installChildEnv(values []string) []string {
	if values == nil {
		values = os.Environ()
	}
	return env.WithInstallChild(values)
}

// newInstallContext derives a context carrying the effective per-app install
// timeout, mirroring binmanager's helper so runtime-managed installs share the
// same deadline semantics. A configured value of 0 disables the deadline: the
// returned context is cancelable but never expires. timeoutSec is returned so
// callers can render a precise "timed out after Ns" message. Callers MUST always
// call cancel (defer cancel()).
// resolveInstallTimeoutSeconds returns the effective per-app install timeout in
// seconds, read through runtimeconfig (the single source of truth) rather than
// env directly. It falls back to a fresh Compute() when runtimeconfig.Init() has
// not run (e.g. unit tests constructing managers directly), mirroring the
// engine's configinputs fallback.
func resolveInstallTimeoutSeconds() int {
	eff, err := runtimeconfig.Get()
	if err != nil {
		eff = runtimeconfig.Compute()
	}
	return eff.InstallTimeoutSeconds
}

func newInstallContext(parent context.Context) (ctx context.Context, cancel context.CancelFunc, timeoutSec int) {
	timeoutSec = resolveInstallTimeoutSeconds()
	if timeoutSec <= 0 {
		ctx, cancel = context.WithCancel(parent)
		return ctx, cancel, 0
	}
	ctx, cancel = context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
	return ctx, cancel, timeoutSec
}

// wrapInstallTimeout turns a context-deadline failure during a runtime app
// install (a download or a subprocess killed by the deadline) into a clear,
// user-facing timeout message. Non-timeout errors (and nil) pass through
// unchanged.
func wrapInstallTimeout(err error, timeoutSec int) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("installation timed out after %ds: %w", timeoutSec, err)
	}
	return err
}

func runInstallCmd(ctx context.Context, cmd *exec.Cmd) error {
	cmd.Env = installChildEnv(cmd.Env)
	if err := installprocess.Run(ctx, cmd); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%w: %w", ctxErr, err)
		}
		return fmt.Errorf("install command failed: %w", err)
	}
	return nil
}

func runInstallCmdStreamingStderr(ctx context.Context, cmd *exec.Cmd, onLine func(string)) (stdout, stderr string, err error) {
	cmd.Env = installChildEnv(cmd.Env)
	stdout, stderr, err = installprocess.RunStreaming(ctx, cmd, onLine)
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("%w: %w", ctx.Err(), err)
	} else if err != nil {
		err = fmt.Errorf("install command failed: %w", err)
	}
	return stdout, stderr, err
}
