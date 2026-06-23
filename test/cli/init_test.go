package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// TestInitHelpGolden freezes `init --help`: a static, offline help block with no
// version or path tokens, so the normalized output equals the raw output. init
// has no subcommands, so there is no subcommand-set drift guard.
func TestInitHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "init", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`init --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`init --help` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "init_help", norm.Apply(res.Stdout))
}

// TestInitDryRunGolden freezes `init --dry-run` against the minimal config: the
// banner, the single "init" frame with "no project types detected", the dry-run
// notice, and the "dry-run" footer. The build version and the run duration are
// masked, so the golden is stable across machines and builds. Dry-run makes no
// network calls and is fully offline.
func TestInitDryRunGolden(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "init", "--dry-run")
	if res.ExitCode != 0 {
		t.Fatalf("`init --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`init --dry-run` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "init_dry_run", norm.Apply(res.Stdout))
}

// TestInitNoopSuccess freezes a real (non-dry-run) `init` against the empty
// minimal config: with no apps/runtimes/tools to download and OCI seeding
// disabled, it is a no-op success (exit 0) ending in the "ready" footer. The
// only filesystem effect is the project's own .datamitsu/ type-definition links;
// nothing is written outside the temp project and the isolated cache (asserted
// via a temp HOME that must stay empty).
func TestInitNoopSuccess(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	home := t.TempDir()
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Env: []string{"HOME=" + home}},
		"--no-auto-config", "--config", cfg, "init")
	if res.ExitCode != 0 {
		t.Fatalf("`init` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`init` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "init_noop", norm.Apply(res.Stdout))

	// The real run lays down .datamitsu/ inside the project (type definitions for
	// IDE config autocomplete) — its one expected write.
	if _, err := os.Stat(filepath.Join(p.Dir, ".datamitsu")); err != nil {
		t.Errorf("`init` did not create the project .datamitsu/ dir: %v", err)
	}
	// Nothing should be written outside the temp project and the isolated cache:
	// the temp HOME must remain untouched.
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read temp HOME: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("`init` wrote unexpectedly into HOME %s: %v", home, entries)
	}
}

// TestInitFlagCombosOffline proves the download-affecting flags (--skip-download,
// --all, --fail-on-download-error) parse and behave offline against the empty
// minimal config. Each yields the same no-op "ready" run as a bare `init`, so we
// assert exit 0 and that the normalized output is byte-identical to a baseline
// `init`. Each case runs in its own fresh project to keep the .datamitsu/ writes
// isolated.
func TestInitFlagCombosOffline(t *testing.T) {
	norm := clitest.NewNormalizer()

	base := clitest.NewProject(t)
	baseCfg := clitest.WriteMinimalConfig(base)
	baseRes := clitest.Run(t, clitest.RunOptions{Dir: base.Dir},
		"--no-auto-config", "--config", baseCfg, "init")
	if baseRes.ExitCode != 0 {
		t.Fatalf("baseline `init` exit = %d, want 0\nstderr:\n%s", baseRes.ExitCode, baseRes.Stderr)
	}
	want := norm.Apply(baseRes.Stdout)

	cases := []struct {
		name string
		flag string
	}{
		{"skip-download", "--skip-download"},
		{"all", "--all"},
		{"fail-on-download-error", "--fail-on-download-error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := clitest.WriteMinimalConfig(p)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, "init", tc.flag)
			if res.ExitCode != 0 {
				t.Fatalf("`init %s` exit = %d, want 0\nstderr:\n%s", tc.flag, res.ExitCode, res.Stderr)
			}
			if got := norm.Apply(res.Stdout); got != want {
				t.Errorf("`init %s` output differs from bare `init`:\n got: %q\nwant: %q", tc.flag, got, want)
			}
		})
	}
}

// TestInitErrorPaths locks the two genuine offline error paths of `init`:
// a --config pointing at a nonexistent file (config load failure) and running
// outside any git repository (git-root resolution failure). Both exit non-zero
// with a descriptive message and never print the usage block (SilenceUsage).
// Note: omitting --config under --no-auto-config is NOT an error — init falls
// back to the embedded default config — so that is intentionally not tested here.
func TestInitErrorPaths(t *testing.T) {
	t.Run("missing-config-file", func(t *testing.T) {
		p := clitest.NewProject(t)
		missing := filepath.Join(p.Dir, "no-such.config.js")

		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", missing, "init", "--dry-run")
		if res.ExitCode == 0 {
			t.Fatalf("`init` with a missing config exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "failed to load config") {
			t.Errorf("stderr should report the config load failure:\n%s", res.Stderr)
		}
		if strings.Contains(res.Stderr, "Usage:") {
			t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
		}
	})

	t.Run("not-a-git-root", func(t *testing.T) {
		// A plain temp dir with no .git anywhere up the tree: GetGitRoot fails, so
		// init refuses before loading any config.
		nonGit, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatalf("eval symlinks: %v", err)
		}
		res := clitest.Run(t, clitest.RunOptions{Dir: nonGit}, "init", "--dry-run")
		if res.ExitCode == 0 {
			t.Fatalf("`init` outside a git repo exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "failed to get git root") {
			t.Errorf("stderr should report the git-root failure:\n%s", res.Stderr)
		}
		if strings.Contains(res.Stderr, "Usage:") {
			t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
		}
	})
}
