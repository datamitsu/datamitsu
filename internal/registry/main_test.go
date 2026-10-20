package registry

import (
	"os"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
)

// TestMain shrinks the retry backoff: every lookup retries transient failures,
// and a test that serves an error must not sit through the production schedule.
func TestMain(m *testing.M) {
	httpretry.RetryBase, httpretry.RetryMax = time.Millisecond, 4*time.Millisecond
	os.Exit(m.Run())
}
