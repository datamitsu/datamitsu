package clitest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/gittest"
	"github.com/datamitsu/datamitsu/internal/toolenv"
)

// DefaultTimeout bounds a single CLI invocation so a hung subprocess fails the
// test instead of blocking the whole suite.
const DefaultTimeout = 60 * time.Second

// interruptGrace is how long an interrupted binary has to stop its tools, which
// it gives five seconds, before it is killed.
const interruptGrace = 10 * time.Second

// RunOptions configures a single subprocess invocation of the datamitsu binary.
type RunOptions struct {
	// Dir is the working directory for the process. Empty means inherit the
	// test's current directory.
	Dir string
	// CacheDir backs DATAMITSU_CACHE_DIR (base for both cache and store). Empty
	// means Run allocates an isolated t.TempDir per call, so runs never touch
	// the developer's real cache/store.
	CacheDir string
	// Env holds extra KEY=VALUE pairs appended after the clean base environment,
	// overriding it on key collision. Use for fixture-specific DATAMITSU_* vars.
	Env []string
	// Stdin, if non-empty, is fed to the process on standard input.
	Stdin string
	// Timeout bounds the run; zero means DefaultTimeout.
	Timeout time.Duration
}

// Result captures the separately-buffered output streams and exit status of a
// single Run.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Err is the raw error from (*exec.Cmd).Run: nil on exit 0, an
	// *exec.ExitError on a non-zero exit, or a start failure otherwise.
	Err error
}

// Run executes the build-once instrumented binary with args in a hermetic,
// offline environment and returns its separated stdout/stderr and exit code.
// Coverage counters flow into the shared GOCOVERDIR. A run that exceeds its
// timeout fails the test rather than returning.
func Run(tb testing.TB, opts RunOptions, args ...string) Result {
	tb.Helper()
	return Start(tb, opts, args...).Wait()
}

// Process is a run of the binary started by Start and not yet waited for.
type Process struct {
	tb             testing.TB
	cmd            *exec.Cmd
	timedOut       func() bool
	cancel         context.CancelFunc
	timeout        time.Duration
	args           []string
	stdout, stderr bytes.Buffer
	waited         bool
}

// Start runs the binary like Run without waiting for it, so a test can act on
// the running process — send it a signal — before collecting its Result with
// Wait. Starting it fails the test.
func Start(tb testing.TB, opts RunOptions, args ...string) *Process {
	tb.Helper()
	bin := BuildOnce(tb)

	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = tb.TempDir()
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	p := &Process{
		tb:       tb,
		timedOut: func() bool { return errors.Is(ctx.Err(), context.DeadlineExceeded) },
		cancel:   cancel,
		timeout:  timeout,
		args:     args,
	}
	// G204: bin is the harness-built binary and args come from test code, not
	// untrusted input.
	p.cmd = exec.CommandContext(ctx, bin, args...) //nolint:gosec
	// The tools run in process groups of their own, so killing the binary
	// would orphan them: a timeout or an early cleanup interrupts it, which
	// makes it stop them, and kills it only if it has not exited by then.
	p.cmd.Cancel = func() error {
		if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
			return p.cmd.Process.Kill()
		}
		return nil
	}
	p.cmd.WaitDelay = interruptGrace
	p.cmd.Dir = opts.Dir
	p.cmd.Env = append(BaseEnv(cacheDir), opts.Env...)
	if opts.Stdin != "" {
		p.cmd.Stdin = strings.NewReader(opts.Stdin)
	}
	p.cmd.Stdout = &p.stdout
	p.cmd.Stderr = &p.stderr

	if err := p.cmd.Start(); err != nil {
		cancel()
		tb.Fatalf("clitest: start `datamitsu %s`: %v", strings.Join(args, " "), err)
	}
	tb.Cleanup(func() {
		if !p.waited {
			cancel()
			_ = p.cmd.Wait()
		}
	})
	return p
}

// Signal delivers sig to the binary alone, not to the tools it started: they
// run in their own process groups, as they do under a terminal's Ctrl-C.
func (p *Process) Signal(sig os.Signal) error {
	if err := p.cmd.Process.Signal(sig); err != nil {
		return fmt.Errorf("clitest: signal %s: %w", sig, err)
	}
	return nil
}

// Wait waits for the process to exit and returns what it wrote and its exit
// code. A process that outlives its timeout fails the test.
func (p *Process) Wait() Result {
	p.tb.Helper()
	defer p.cancel()

	p.waited = true
	err := p.cmd.Wait()
	if p.timedOut() {
		p.tb.Fatalf("clitest: `datamitsu %s` timed out after %s\n--- stdout ---\n%s\n--- stderr ---\n%s",
			strings.Join(p.args, " "), p.timeout, p.stdout.String(), p.stderr.String())
	}

	return Result{
		Stdout:   p.stdout.String(),
		Stderr:   p.stderr.String(),
		ExitCode: ExitCodeOf(err),
		Err:      err,
	}
}

// BaseEnv returns a clean, deterministic environment for a subprocess run.
// Inherited variables that steer datamitsu or the tools it runs are stripped
// (see strippedKey), so configuration and mode detection are fully controlled
// by the harness, and the binary runs offline with an isolated cache rooted at
// cacheDir. The returned slice is freshly allocated; callers may append
// overrides to it.
func BaseEnv(cacheDir string) []string {
	const sep = "="
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, sep)
		if strippedKey(key) {
			continue
		}
		env = append(env, kv)
	}
	// Deterministic, hermetic, offline. GOCOVERDIR routes counters to the shared
	// cover dir; NO_COLOR + piped (non-TTY) streams force plain output;
	// SOURCE_DATE_EPOCH fixes the time a report is stamped with. A developer's
	// personal git ignore file must not change what the binary walks.
	env = append(env,
		"GOCOVERDIR="+CoverDir(),
		"NO_COLOR=1",
		"SOURCE_DATE_EPOCH="+SourceDateEpoch,
		"DATAMITSU_CACHE_DIR="+cacheDir,
		"DATAMITSU_OFFLINE=1",
		"DATAMITSU_NO_OCI=1",
	)
	return append(env, gittest.Env()...)
}

// strippedKey reports whether an inherited environment variable must be dropped
// from the clean base env: every DATAMITSU_* var (so the harness is the only
// source of datamitsu config), the variables that steer mode, color or output
// detection in datamitsu or in the tools it runs (ambientKeys, and everything
// toolenv strips from a tool), inherited command-scope git config (which would
// outrank gittest.Env), and the keys BaseEnv sets explicitly (avoid duplicate,
// ambiguous entries). A golden recorded inside a CI job or an agent session
// would otherwise differ from one recorded in a plain shell; a scenario that
// needs one of these variables sets it through RunOptions.Env.
func strippedKey(key string) bool {
	if _, ok := ambientKeys[key]; ok {
		return true
	}
	if strings.HasPrefix(key, "DATAMITSU_") || toolenv.Stripped(key) {
		return true
	}
	return gittest.IsCommandScopeKey(key)
}

// ambientKeys are stripped by exact name on top of toolenv's list: CI and
// terminal detection, and the CI-system markers datamitsu itself may read.
var ambientKeys = map[string]struct{}{
	"CI": {}, "TERM": {}, "NO_COLOR": {}, "GOCOVERDIR": {},
	"TF_BUILD": {}, "TEAMCITY_VERSION": {}, "SOURCE_DATE_EPOCH": {},
}

// SourceDateEpoch is the SOURCE_DATE_EPOCH every run gets: 2023-11-14T22:13:20Z.
const SourceDateEpoch = "1700000000"

// ExitCodeOf extracts the process exit code from an error returned by
// (*exec.Cmd).Run: 0 for nil, the real code for an *exec.ExitError, and -1 for
// any other failure (e.g. the binary could not be started).
func ExitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}
