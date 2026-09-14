package runner

import (
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gittest"
)

// TestMain drops git's repository-discovery variables so the tests below act on
// their own temporary repositories. Without it the staged-file tests stage and
// commit into the real checkout instead of their fixture; see internal/gitenv.
// A personal git ignore file is kept out too, since discovery honors it.
func TestMain(m *testing.M) {
	gittest.Isolate()
	os.Exit(m.Run())
}
