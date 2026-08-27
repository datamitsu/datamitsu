package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/configcache"
	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// autoDiscoveredMinimalConfigJS is the no-op config under the name discovery
// looks for, so the load is a repository chain rather than a machine-level one.
const autoDiscoveredMinimalConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({ apps: {}, runtimes: {}, setup: {}, tools: {} });
globalThis.getMinVersion = () => "0.0.0";
`

// configEvalArtifacts lists the artifact files of the config-evaluation cache
// under an isolated DATAMITSU_CACHE_DIR, sorted by path.
func configEvalArtifacts(tb testing.TB, cacheDir string) []string {
	tb.Helper()
	root := filepath.Join(cacheDir, "cache", configcache.DirName)
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// A tree that was never created is simply no artifacts.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() && filepath.Ext(p) == ".msgpack" {
			out = append(out, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		tb.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// gitIn runs git inside dir, failing the test on error. Branch switching is not
// part of the clitest Project surface and only these tests need it.
func gitIn(tb testing.TB, dir string, args ...string) {
	tb.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitenv.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// TestConfigShowIdenticalAcrossCacheMissAndHit is the acceptance property of
// the config-evaluation cache: a hit must be observationally identical to the
// miss that filled it. Both streams are compared, because a cache that changed
// only stderr would still make a command's output depend on cache warmth.
func TestConfigShowIdenticalAcrossCacheMissAndHit(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir}

	miss := clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "config", "show")
	if miss.ExitCode != 0 {
		t.Fatalf("cold `config show` exit = %d, want 0\nstderr:\n%s", miss.ExitCode, miss.Stderr)
	}
	if got := configEvalArtifacts(t, cacheDir); len(got) != 1 {
		t.Fatalf("cold run stored %d artifacts, want 1: %v", len(got), got)
	}

	hit := clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "config", "show")
	if hit.ExitCode != miss.ExitCode {
		t.Fatalf("warm exit = %d, cold exit = %d", hit.ExitCode, miss.ExitCode)
	}
	if hit.Stdout != miss.Stdout {
		t.Errorf("`config show` stdout differs between a miss and a hit:\ncold:\n%s\nwarm:\n%s",
			miss.Stdout, hit.Stdout)
	}
	if hit.Stderr != miss.Stderr {
		t.Errorf("`config show` stderr differs between a miss and a hit:\ncold:\n%s\nwarm:\n%s",
			miss.Stderr, hit.Stderr)
	}
}

// TestConfigCacheSecondInvocationWritesNothing asserts a hit is a pure read: an
// identical second invocation must not rewrite the artifact. Rewriting would be
// harmless for correctness and wrong for cost — it would put a msgpack marshal
// back on the fast path.
func TestConfigCacheSecondInvocationWritesNothing(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir}

	if res := clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "config", "show"); res.ExitCode != 0 {
		t.Fatalf("cold run exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	artifacts := configEvalArtifacts(t, cacheDir)
	if len(artifacts) != 1 {
		t.Fatalf("cold run stored %d artifacts, want 1: %v", len(artifacts), artifacts)
	}
	before, err := os.Stat(artifacts[0])
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}

	// The hit path refreshes an entry's mtime only once it is older than a day,
	// so a second run seconds later must leave it exactly as it was.
	time.Sleep(10 * time.Millisecond)
	if res := clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "config", "show"); res.ExitCode != 0 {
		t.Fatalf("warm run exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	after, err := os.Stat(artifacts[0])
	if err != nil {
		t.Fatalf("stat artifact after the warm run: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("a second identical invocation rewrote the artifact: mtime %v -> %v",
			before.ModTime(), after.ModTime())
	}
	if got := configEvalArtifacts(t, cacheDir); len(got) != 1 {
		t.Errorf("warm run left %d artifacts, want 1: %v", len(got), got)
	}
}

// TestConfigCacheTreeBoundedAcrossBranches asserts the tree grows with the
// number of distinct chains, not with the number of invocations: 21 loads
// across 3 branches must leave 3 artifacts. HEAD is in the key of a
// repository chain, so each branch gets its own entry — and each branch's
// repeat loads reuse it. The config is auto-discovered here on purpose: an
// explicit --config chain is machine-level and has no git root to be keyed by.
func TestConfigCacheTreeBoundedAcrossBranches(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", autoDiscoveredMinimalConfigJS)
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir}

	branches := []string{"cache-a", "cache-b", "cache-c"}
	for _, branch := range branches {
		gitIn(t, p.Dir, "checkout", "-b", branch)
		for range 7 {
			res := clitest.Run(t, opts, "config", "show")
			if res.ExitCode != 0 {
				t.Fatalf("`config show` on %s exit = %d, want 0\nstderr:\n%s",
					branch, res.ExitCode, res.Stderr)
			}
		}
	}

	got := configEvalArtifacts(t, cacheDir)
	if len(got) != len(branches) {
		t.Errorf("21 invocations across %d branches left %d artifacts, want %d:\n%v",
			len(branches), len(got), len(branches), got)
	}
}

// TestConfigCacheDisabledStoresNothing pins the escape hatch: with
// DATAMITSU_CONFIG_CACHE=0 the tree is never created, so a user who turns the
// cache off has no cache to go stale.
func TestConfigCacheDisabledStoresNothing(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	cacheDir := t.TempDir()
	opts := clitest.RunOptions{Dir: p.Dir, CacheDir: cacheDir, Env: []string{"DATAMITSU_CONFIG_CACHE=0"}}

	for range 2 {
		res := clitest.Run(t, opts, "--no-auto-config", "--config", cfg, "config", "show")
		if res.ExitCode != 0 {
			t.Fatalf("`config show` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
	}
	if got := configEvalArtifacts(t, cacheDir); len(got) != 0 {
		t.Errorf("cache disabled but %d artifacts were stored: %v", len(got), got)
	}
}
