// Package cli_test holds the offline, subprocess-based golden contract tests
// that freeze datamitsu's externally-observable CLI behavior. TestMain builds
// the coverage-instrumented binary once before any test runs and converts the
// accumulated GOCOVERDIR counters into a text profile afterwards for CI merge.
package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestMain(m *testing.M) {
	if _, err := clitest.EnsureBuilt(); err != nil {
		fmt.Fprintln(os.Stderr, "clitest: build failed:", err)
		os.Exit(1)
	}

	code := m.Run()

	// Convert subprocess coverage to a text profile for the CI merge step.
	// DATAMITSU_TEST_COVER_OUT pins the destination; default to a file in the
	// shared cover dir so the data is discoverable without extra config.
	out := os.Getenv("DATAMITSU_TEST_COVER_OUT")
	if out == "" {
		out = filepath.Join(clitest.CoverDir(), "cli-coverage.txt")
	}
	if err := clitest.WriteCoverProfile(out); err != nil {
		fmt.Fprintln(os.Stderr, "clitest: write coverage profile:", err)
	}

	os.Exit(code)
}
