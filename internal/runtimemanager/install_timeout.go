package runtimemanager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// install_timeout.go threads the per-app install timeout (the effective
// InstallTimeoutSeconds from runtimeconfig) through the runtime-managed app
// installs the way binmanager does for binary downloads. The runtime layer
// additionally spawns subprocesses (uv sync, pnpm install, go build), so it
// carries both an HTTP-download path and a subprocess path off the same deadline.

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

// runInstallCmd runs an install subprocess under ctx and, when the context
// deadline killed it, surfaces context.DeadlineExceeded. This is necessary
// because exec.CommandContext kills the process with SIGKILL and cmd.Run then
// reports a generic "signal: killed" that does NOT wrap the context error — the
// context state is the reliable timeout signal. Wrapping ctx.Err() lets the
// install boundary's wrapInstallTimeout recognize the deadline and render the
// "installation timed out after Ns" message. A non-timeout failure is returned
// as-is.
func runInstallCmd(ctx context.Context, cmd *exec.Cmd) error {
	err := cmd.Run()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%w: %w", ctxErr, err)
		}
		return fmt.Errorf("install command failed: %w", err)
	}
	return nil
}
