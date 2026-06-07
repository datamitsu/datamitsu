package runtimemanager

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
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

// runInstallCmdStreaming runs an install subprocess like runInstallCmd, but
// streams stdout line by line to onLine (for live progress / event parsing)
// while capturing stderr for error display. It returns the captured stderr and
// the run error (with the same context-deadline wrapping as runInstallCmd).
//
// stdout is read to EOF before Wait is called (the canonical StdoutPipe order),
// so onLine sees every line and there is no read/Wait race. stderr is the only
// writer to the returned buffer and is joined by Wait, so a plain buffer is
// safe.
func runInstallCmdStreaming(ctx context.Context, cmd *exec.Cmd, onLine func([]byte)) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("install command stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return stderr.String(), fmt.Errorf("install command failed: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if onLine != nil {
			onLine(scanner.Bytes())
		}
	}

	runErr := cmd.Wait()
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stderr.String(), fmt.Errorf("%w: %w", ctxErr, runErr)
		}
		return stderr.String(), fmt.Errorf("install command failed: %w", runErr)
	}
	return stderr.String(), nil
}

// runInstallCmdStreamingStderr is the mirror of runInstallCmdStreaming for tools
// (uv) that emit their machine-readable summary on stdout and human progress on
// stderr: it fully captures stdout (returned for parsing) while streaming stderr
// line by line to onLine for live progress. Both pipes are drained concurrently
// to avoid a full-pipe deadlock. The run error carries the same context-deadline
// wrapping as runInstallCmd.
func runInstallCmdStreamingStderr(ctx context.Context, cmd *exec.Cmd, onLine func(string)) (stdout, stderr string, err error) {
	stdoutPipe, perr := cmd.StdoutPipe()
	if perr != nil {
		return "", "", fmt.Errorf("install command stdout pipe: %w", perr)
	}
	stderrPipe, perr := cmd.StderrPipe()
	if perr != nil {
		return "", "", fmt.Errorf("install command stderr pipe: %w", perr)
	}
	if perr := cmd.Start(); perr != nil {
		return "", "", fmt.Errorf("install command failed: %w", perr)
	}

	var outBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(&outBuf, stdoutPipe)
	})

	var errBuf bytes.Buffer
	scanner := bufio.NewScanner(stderrPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		errBuf.WriteString(line)
		errBuf.WriteByte('\n')
		if onLine != nil {
			onLine(line)
		}
	}

	wg.Wait()
	runErr := cmd.Wait()
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return outBuf.String(), errBuf.String(), fmt.Errorf("%w: %w", ctxErr, runErr)
		}
		return outBuf.String(), errBuf.String(), fmt.Errorf("install command failed: %w", runErr)
	}
	return outBuf.String(), errBuf.String(), nil
}
