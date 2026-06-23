package clitest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// Build-once state. The instrumented binary is expensive to produce, so the
// whole test process shares a single build guarded by sync.Once.
var (
	buildOnce   sync.Once
	builtBinary string
	errBuild    error
)

// Shared GOCOVERDIR state. Every subprocess invocation writes its coverage
// counters into this one directory so TestMain can convert them in bulk.
var (
	coverDirOnce sync.Once
	coverDirPath string
	errCoverDir  error
)

// BuildOnce builds the coverage-instrumented datamitsu binary exactly once for
// the current test process and returns its path. Concurrent callers share the
// same build. It fails the test (not the whole process) on error.
func BuildOnce(tb testing.TB) string {
	tb.Helper()
	path, err := EnsureBuilt()
	if err != nil {
		tb.Fatalf("clitest: %v", err)
	}
	return path
}

// EnsureBuilt is the TestMain-friendly variant of BuildOnce: it performs the
// guarded build without requiring a *testing.T and returns (path, error).
func EnsureBuilt() (string, error) {
	buildOnce.Do(func() {
		builtBinary, errBuild = buildInstrumented()
	})
	return builtBinary, errBuild
}

// CoverDir returns the shared directory that instrumented subprocess runs write
// their coverage counters into (via the GOCOVERDIR env var). It is created
// lazily on first use. When DATAMITSU_TEST_COVERDIR is set it points there (so a
// combined-coverage runner can merge these raw counters with unit-test covdata —
// see scripts/coverage-all.sh); otherwise a fresh temp dir is used. TestMain
// converts its contents into a text profile via WriteCoverProfile after the
// suite runs.
func CoverDir() string {
	coverDirOnce.Do(func() {
		// forbidigo: harness-only knob read in the parent test process to pin the
		// raw covdata location for the combined-coverage merge; not a datamitsu
		// runtime config var. The subprocess never sees it (BaseEnv strips all
		// DATAMITSU_* and sets GOCOVERDIR to this path explicitly).
		if dir := os.Getenv("DATAMITSU_TEST_COVERDIR"); dir != "" { //nolint:forbidigo
			// G703: dir is a developer-supplied test knob, not untrusted input.
			errCoverDir = os.MkdirAll(dir, 0o755) //nolint:gosec
			coverDirPath = dir
			return
		}
		coverDirPath, errCoverDir = os.MkdirTemp("", "datamitsu-cli-cover-*")
	})
	if errCoverDir != nil {
		panic(fmt.Sprintf("clitest: prepare cover dir: %v", errCoverDir))
	}
	return coverDirPath
}

// WriteCoverProfile converts the accumulated GOCOVERDIR counters into a text
// coverage profile at outPath, suitable for merging with unit-test coverage.
// It is a no-op (returns nil) when no coverage data was collected, so it is
// safe to call unconditionally from TestMain.
func WriteCoverProfile(outPath string) error {
	dir := CoverDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read cover dir: %w", err)
	}
	if len(entries) == 0 {
		return nil // nothing was collected; not an error
	}
	// G204: fixed go-tool invocation; dir is a harness-owned temp dir and
	// outPath is supplied by the test harness, not untrusted input.
	cmd := exec.CommandContext(context.Background(), "go", "tool", "covdata", "textfmt", "-i="+dir, "-o="+outPath) //nolint:gosec
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("covdata textfmt: %w\n%s", err, out)
	}
	return nil
}

// buildInstrumented compiles the datamitsu binary with coverage instrumentation
// into a fresh temp dir and returns its path.
func buildInstrumented() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	binDir, err := os.MkdirTemp("", "datamitsu-cli-bin-*")
	if err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}
	bin := filepath.Join(binDir, "datamitsu")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// G204: fixed `go build` invocation at the module root; bin is a
	// harness-owned temp path, not untrusted input.
	cmd := exec.CommandContext(context.Background(), "go", "build", "-cover", "-covermode=atomic", "-o", bin, ".") //nolint:gosec
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build -cover failed: %w\n%s", err, out)
	}
	return bin, nil
}

// moduleRoot walks up from the current working directory until it finds the
// directory containing go.mod (the module root, where `go build .` is valid).
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found walking up from working directory")
		}
		dir = parent
	}
}
