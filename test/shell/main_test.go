package shell_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/gitenv"
)

func TestMain(m *testing.M) {
	// The tier switches branches inside temporary git repositories; git's
	// repository-discovery variables would point it at the real checkout when
	// the suite itself runs from a git hook. See internal/gitenv.
	gitenv.Unset()

	if _, err := clitest.EnsureBuilt(); err != nil {
		fmt.Fprintln(os.Stderr, "clitest: build failed:", err)
		os.Exit(1)
	}

	code := m.Run()

	// The shells here exec the same instrumented binary the CLI suite builds, so
	// the shim's dispatch path shows up in the merged coverage profile. The
	// destination is not overridable: this tier and test/cli share one GOCOVERDIR
	// under scripts/coverage-all.sh, and a shared override would have them
	// clobber each other's profile.
	out := filepath.Join(clitest.CoverDir(), "shell-coverage.txt")
	if err := clitest.WriteCoverProfile(out); err != nil {
		fmt.Fprintln(os.Stderr, "clitest: write coverage profile:", err)
	}

	os.Exit(code)
}
