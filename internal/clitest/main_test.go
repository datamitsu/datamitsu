package clitest

import (
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// TestMain drops git's repository-discovery variables so the tests below act on
// their own temporary repositories. See internal/gitenv for why the pre-commit
// hook makes this necessary.
func TestMain(m *testing.M) {
	gitenv.Unset()
	os.Exit(m.Run())
}
