package binmanager

import (
	"os"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/launch"
)

// TestMain shrinks the retry backoff: downloads retry transient failures, and
// a test that serves an error must not sit through the production schedule.
func TestMain(m *testing.M) {
	if code, handled := launch.Dispatch(); handled {
		os.Exit(code)
	}
	httpretry.RetryBase, httpretry.RetryMax = time.Millisecond, 4*time.Millisecond
	os.Exit(m.Run())
}
