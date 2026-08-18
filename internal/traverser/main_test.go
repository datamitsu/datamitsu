package traverser

import (
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// TestMain drops git's repository-discovery variables so the tests below act on
// their own temporary repositories. The pre-commit hook (lefthook.yaml) runs the
// suite with GIT_DIR pointing at the real checkout, and git would otherwise
// prefer it over the directory each test hands to git.
func TestMain(m *testing.M) {
	gitenv.Unset()
	os.Exit(m.Run())
}
