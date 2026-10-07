package cmd

import (
	"os"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/gittest"
	"github.com/datamitsu/datamitsu/internal/httpretry"
)

// TestMain drops git's repository-discovery variables so the tests below act on
// their own temporary repositories. See internal/gitenv for why the pre-commit
// hook makes this necessary. A personal git ignore file is kept out too, since
// discovery honors it.
//
// It also points the cache tree at a temp directory for the whole package: every
// config load now writes an evaluated-config artifact, and a test that does not
// call isolateCacheTree would otherwise store into the developer's real cache
// and — worse — could be served from it.
func TestMain(m *testing.M) {
	gittest.Isolate()

	cacheDir, err := os.MkdirTemp("", "datamitsu-cmd-cache-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("DATAMITSU_CACHE_DIR", cacheDir); err != nil {
		panic(err)
	}
	// Every registry and GitHub request retries transient failures; a test
	// that serves an error must not sit through the production backoff.
	httpretry.RetryBase, httpretry.RetryMax = time.Millisecond, 4*time.Millisecond

	code := m.Run()
	_ = os.RemoveAll(cacheDir)
	os.Exit(code)
}
