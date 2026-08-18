package runner

import (
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// TestMain drops git's repository-discovery variables so the tests below act on
// their own temporary repositories. Without it the staged-file tests stage and
// commit into the real checkout instead of their fixture; see internal/gitenv.
func TestMain(m *testing.M) {
	gitenv.Unset()
	os.Exit(m.Run())
}
