package bundled

import (
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gittest"
)

// TestMain keeps the developer's git setup out of the fixtures below: their
// directories carry a .git entry, so discovery would otherwise apply a personal
// git ignore file to them. See internal/gittest.
func TestMain(m *testing.M) {
	gittest.Isolate()
	os.Exit(m.Run())
}
