package traverser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gitenv"
	"github.com/datamitsu/datamitsu/internal/gittest"
)

// TestMain drops git's repository-discovery variables so the tests below act on
// their own temporary repositories. The pre-commit hook (lefthook.yaml) runs the
// suite with GIT_DIR pointing at the real checkout, and git would otherwise
// prefer it over the directory each test hands to git.
//
// It also hides the developer's global and system git config and XDG config
// home, and any inherited command-scope config: the walker honors
// core.excludesFile, so a personal ignore file would otherwise change what the
// tests see. Unlike gittest.Isolate this leaves core.excludesFile settable,
// because the tests here set it themselves.
func TestMain(m *testing.M) {
	gitenv.Unset()
	gittest.ClearCommandScope()
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	dir, err := os.MkdirTemp("", "traverser-xdg-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))

	return m.Run()
}
