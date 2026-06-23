package clitest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// DefaultTimeout bounds a single CLI invocation so a hung subprocess fails the
// test instead of blocking the whole suite.
const DefaultTimeout = 60 * time.Second

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
	defer cancel()

	// G204: bin is the harness-built binary and args come from test code, not
	// untrusted input.
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec
	cmd.Dir = opts.Dir
	cmd.Env = append(BaseEnv(cacheDir), opts.Env...)
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		tb.Fatalf("clitest: `datamitsu %s` timed out after %s\n--- stdout ---\n%s\n--- stderr ---\n%s",
			strings.Join(args, " "), timeout, stdout.String(), stderr.String())
	}

	return Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: ExitCodeOf(err),
		Err:      err,
	}
}

// BaseEnv returns a clean, deterministic environment for a subprocess run.
// Inherited DATAMITSU_* vars (plus CI and TERM) are stripped so configuration
// and mode detection are fully controlled by the harness, and the binary runs
// offline with an isolated cache rooted at cacheDir. The returned slice is
// freshly allocated; callers may append overrides to it.
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
	// cover dir; NO_COLOR + piped (non-TTY) streams force plain output.
	env = append(env,
		"GOCOVERDIR="+CoverDir(),
		"NO_COLOR=1",
		"DATAMITSU_CACHE_DIR="+cacheDir,
		"DATAMITSU_OFFLINE=1",
		"DATAMITSU_NO_OCI=1",
	)
	return env
}

// strippedKey reports whether an inherited environment variable must be dropped
// from the clean base env. We strip every DATAMITSU_* var (so the harness is the
// only source of datamitsu config), CI/TERM (which steer mode/output detection),
// and the keys BaseEnv sets explicitly (avoid duplicate, ambiguous entries).
func strippedKey(key string) bool {
	switch key {
	case "CI", "TERM", "NO_COLOR", "GOCOVERDIR":
		return true
	}
	return strings.HasPrefix(key, "DATAMITSU_")
}

// ExitCodeOf extracts the process exit code from an error returned by
// (*exec.Cmd).Run: 0 for nil, the real code for an *exec.ExitError, and -1 for
// any other failure (e.g. the binary could not be started).
func ExitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
