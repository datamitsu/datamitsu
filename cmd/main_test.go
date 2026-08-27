package cmd

import (
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// TestMain drops git's repository-discovery variables so the tests below act on
// their own temporary repositories. See internal/gitenv for why the pre-commit
// hook makes this necessary.
//
// It also points the cache tree at a temp directory for the whole package: every
// config load now writes an evaluated-config artifact, and a test that does not
// call isolateCacheTree would otherwise store into the developer's real cache
// and — worse — could be served from it.
func TestMain(m *testing.M) {
	gitenv.Unset()

	cacheDir, err := os.MkdirTemp("", "datamitsu-cmd-cache-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("DATAMITSU_CACHE_DIR", cacheDir); err != nil {
		panic(err)
	}

	code := m.Run()
	_ = os.RemoveAll(cacheDir)
	os.Exit(code)
}
