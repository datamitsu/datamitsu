package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"github.com/datamitsu/datamitsu/internal/env"
)

// expectedCacheSubcommands is the exact set of `cache` leaf commands (drift
// guard): a new cache subcommand must be added here deliberately, with its own
// blackbox test. `path project` is nested under `path`, so it does not appear in
// the top-level `cache` listing.
var expectedCacheSubcommands = []string{
	"clear",
	"path",
}

// TestCacheHelpGolden freezes `cache --help`: a static, offline help block with
// no version or path tokens, so the normalized output equals the raw output. The
// subcommand set is additionally compared as a set to decouple the drift guard
// from help formatting.
func TestCacheHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "cache", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`cache --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`cache --help` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "cache_help", norm.Apply(res.Stdout))

	got := parseAvailableCommands(res.Stdout)
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(expectedCacheSubcommands), ",") {
		t.Errorf("cache subcommand set drift:\n got: %v\nwant: %v", got, expectedCacheSubcommands)
	}
}

// TestCachePath freezes `cache path`: the printed path is the cache subdir
// ({base}/cache) of DATAMITSU_CACHE_DIR. We pin the cache base to a known temp
// dir, mask it to <CACHE>, and golden the masked line; an exact equality check
// against the computed path guards the contract independently of the golden.
func TestCachePath(t *testing.T) {
	cacheBase := t.TempDir()
	norm := clitest.NewNormalizer().MaskPath(cacheBase, "<CACHE>")

	res := clitest.Run(t, clitest.RunOptions{CacheDir: cacheBase}, "cache", "path")
	if res.ExitCode != 0 {
		t.Fatalf("`cache path` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`cache path` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "cache_path", norm.Apply(res.Stdout))

	wantPath := filepath.Join(cacheBase, "cache")
	if got := strings.TrimRight(res.Stdout, "\n"); got != wantPath {
		t.Errorf("`cache path` = %q, want %q", got, wantPath)
	}
}

// TestCachePathProject locks `cache path project`. Inside a git project the
// printed path is the per-project cache directory keyed by the XXH3 hash of the
// git root. Outside any git repo the command still succeeds, falling back to the
// CWD as the project root (it does NOT error). The only error path is a present
// but unusable .git — exercised by TestCacheProjectResolutionFailure.
func TestCachePathProject(t *testing.T) {
	t.Run("inside-git", func(t *testing.T) {
		cacheBase := t.TempDir()
		p := clitest.NewProject(t)

		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheBase},
			"cache", "path", "project")
		if res.ExitCode != 0 {
			t.Fatalf("`cache path project` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		want := filepath.Join(cacheBase, "cache", "projects", env.HashProjectPath(p.Dir), "cache")
		if got := strings.TrimRight(res.Stdout, "\n"); got != want {
			t.Errorf("`cache path project` = %q, want %q", got, want)
		}
	})

	t.Run("outside-git", func(t *testing.T) {
		cacheBase := t.TempDir()
		// A plain temp dir with no .git anywhere up the tree: resolveProjectRoot
		// falls back to CWD, so the command succeeds with a CWD-keyed path.
		nonGit, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatalf("eval symlinks: %v", err)
		}
		res := clitest.Run(t, clitest.RunOptions{Dir: nonGit, CacheDir: cacheBase},
			"cache", "path", "project")
		if res.ExitCode != 0 {
			t.Fatalf("`cache path project` (no git) exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		want := filepath.Join(cacheBase, "cache", "projects", env.HashProjectPath(nonGit), "cache")
		if got := strings.TrimRight(res.Stdout, "\n"); got != want {
			t.Errorf("`cache path project` (no git) = %q, want %q", got, want)
		}
	})
}

// TestCacheClearDryRun freezes the `--dry-run` messages for both the
// current-project and `--all` variants and proves nothing is deleted: a sentinel
// file planted in the cache survives the run. The project hash and cache base are
// masked so the golden is stable across machines.
func TestCacheClearDryRun(t *testing.T) {
	cacheBase := t.TempDir()
	p := clitest.NewProject(t)

	// Plant a sentinel inside the project cache dir that dry-run claims it "would
	// delete"; it must still exist afterward.
	hash := env.HashProjectPath(p.Dir)
	projectsDir := filepath.Join(cacheBase, "cache", "projects")
	sentinel := filepath.Join(projectsDir, hash, "sentinel.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatalf("mkdir sentinel dir: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	norm := clitest.NewNormalizer().MaskPath(cacheBase, "<CACHE>").MaskPath(hash, "<HASH>")

	t.Run("project", func(t *testing.T) {
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheBase},
			"cache", "clear", "--dry-run")
		if res.ExitCode != 0 {
			t.Fatalf("`cache clear --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		clitest.AssertGolden(t, "cache_clear_dry_run", norm.Apply(res.Stdout))
	})

	t.Run("all", func(t *testing.T) {
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, CacheDir: cacheBase},
			"cache", "clear", "--all", "--dry-run")
		if res.ExitCode != 0 {
			t.Fatalf("`cache clear --all --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		clitest.AssertGolden(t, "cache_clear_all_dry_run", norm.Apply(res.Stdout))
	})

	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("dry-run deleted the sentinel %s: %v", sentinel, err)
	}
}

// TestCacheProjectResolutionFailure locks the one genuine error path of the
// project-scoped cache commands: a `.git` entry that exists but is not a usable
// repository. resolveProjectRoot refuses to silently fall back to CWD (which
// would compute a wrong cache key) and surfaces a non-zero error on stderr with
// no usage block (SilenceUsage).
func TestCacheProjectResolutionFailure(t *testing.T) {
	for _, args := range [][]string{
		{"cache", "path", "project"},
		{"cache", "clear", "--dry-run"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatalf("eval symlinks: %v", err)
			}
			// A plain file named .git: HasGitDir reports true, but `git rev-parse`
			// fails, so the root cannot be determined.
			if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not a git dir"), 0o644); err != nil {
				t.Fatalf("write bogus .git: %v", err)
			}

			res := clitest.Run(t, clitest.RunOptions{Dir: dir}, args...)
			if res.ExitCode == 0 {
				t.Fatalf("`%s` exit = 0, want non-zero\nstdout:\n%s", strings.Join(args, " "), res.Stdout)
			}
			if !strings.Contains(res.Stderr, "failed to determine git root") {
				t.Errorf("stderr should name the git-root failure:\n%s", res.Stderr)
			}
			if strings.Contains(res.Stderr, "Usage:") {
				t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
			}
		})
	}
}
