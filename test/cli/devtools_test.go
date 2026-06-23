package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// expectedDevtoolsSubcommands is the exact set of `devtools` leaf/group commands.
// A drift guard: a new devtools subcommand must be added here deliberately, with
// its own blackbox test, rather than slipping into the contract untested.
var expectedDevtoolsSubcommands = []string{
	"apps",
	"bundles",
	"dockerfile",
	"pack-inline-archive",
	"pull-github",
	"pull-node",
	"pull-runtimes",
	"pull-uv",
	"split-config",
	"verify-all",
}

// expectedAppsSubcommands / expectedBundlesSubcommands are drift guards for the
// two inspection groups (identical command sets, different targets).
var (
	expectedAppsSubcommands    = []string{"inspect", "list", "path"}
	expectedBundlesSubcommands = []string{"inspect", "list", "path"}
)

// devtoolsHelpCases enumerates every devtools help surface. Each help block is
// static — no temp paths, durations, or real version tokens — so the goldens are
// the raw stdout (un-normalized). Normalizing would be actively wrong here: the
// build version defaults to "dev", and pull-runtimes' help mentions "go.dev",
// whose "dev" the version rule would mask to "<VERSION>", making that golden
// build-dependent.
var devtoolsHelpCases = []struct {
	name   string   // golden file name
	args   []string // args after the binary
	golden string
}{
	{"devtools", []string{"devtools", "--help"}, "devtools_help"},
	{"apps", []string{"devtools", "apps", "--help"}, "devtools_apps_help"},
	{"bundles", []string{"devtools", "bundles", "--help"}, "devtools_bundles_help"},
	{"dockerfile", []string{"devtools", "dockerfile", "--help"}, "devtools_dockerfile_help"},
	{"split-config", []string{"devtools", "split-config", "--help"}, "devtools_split_config_help"},
	{"pull-github", []string{"devtools", "pull-github", "--help"}, "devtools_pull_github_help"},
	{"pull-node", []string{"devtools", "pull-node", "--help"}, "devtools_pull_node_help"},
	{"pull-uv", []string{"devtools", "pull-uv", "--help"}, "devtools_pull_uv_help"},
	{"pull-runtimes", []string{"devtools", "pull-runtimes", "--help"}, "devtools_pull_runtimes_help"},
	{"verify-all", []string{"devtools", "verify-all", "--help"}, "devtools_verify_all_help"},
	{"pack-inline-archive", []string{"devtools", "pack-inline-archive", "--help"}, "devtools_pack_inline_archive_help"},
}

// TestDevtoolsHelpGolden freezes `devtools --help` and `--help` for every
// subcommand: exit 0, nothing on stderr, and a normalized==raw static help block.
func TestDevtoolsHelpGolden(t *testing.T) {
	for _, tc := range devtoolsHelpCases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{}, tc.args...)
			if res.ExitCode != 0 {
				t.Fatalf("`%s` exit = %d, want 0\nstderr:\n%s",
					strings.Join(tc.args, " "), res.ExitCode, res.Stderr)
			}
			if res.Stderr != "" {
				t.Errorf("`%s` wrote to stderr:\n%s", strings.Join(tc.args, " "), res.Stderr)
			}
			clitest.AssertGolden(t, tc.golden, res.Stdout)
		})
	}
}

// TestDevtoolsCommandSetDrift asserts the devtools / apps / bundles subcommand
// sets are exactly the expected sets, decoupled from help formatting.
func TestDevtoolsCommandSetDrift(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"devtools", []string{"devtools", "--help"}, expectedDevtoolsSubcommands},
		{"apps", []string{"devtools", "apps", "--help"}, expectedAppsSubcommands},
		{"bundles", []string{"devtools", "bundles", "--help"}, expectedBundlesSubcommands},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{}, tc.args...)
			if res.ExitCode != 0 {
				t.Fatalf("`%s` exit = %d, want 0\nstderr:\n%s",
					strings.Join(tc.args, " "), res.ExitCode, res.Stderr)
			}
			got := parseAvailableCommands(res.Stdout)
			if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(tc.want), ",") {
				t.Errorf("%s subcommand set drift:\n got: %v\nwant: %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestDevtoolsArgValidation locks the offline arg/flag-validation contract:
// every misuse exits non-zero with a descriptive message on stderr and — because
// the root sets SilenceUsage — never prints the usage block. None of these touch
// the network (validation happens before any command body runs).
func TestDevtoolsArgValidation(t *testing.T) {
	p := clitest.NewProject(t)

	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{
			name:    "dockerfile-missing-output",
			args:    []string{"devtools", "dockerfile"},
			wantMsg: `required flag(s) "output" not set`,
		},
		{
			name:    "split-config-missing-output",
			args:    []string{"devtools", "split-config"},
			wantMsg: `required flag(s) "output" not set`,
		},
		{
			name:    "pull-runtimes-no-arg",
			args:    []string{"devtools", "pull-runtimes"},
			wantMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:    "pull-runtimes-requires-update",
			args:    []string{"devtools", "pull-runtimes", "runtimes.json"},
			wantMsg: "--update flag is required",
		},
		{
			name:    "pull-github-no-arg",
			args:    []string{"devtools", "pull-github"},
			wantMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:    "pull-node-no-arg",
			args:    []string{"devtools", "pull-node"},
			wantMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:    "pull-uv-no-arg",
			args:    []string{"devtools", "pull-uv"},
			wantMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:    "pack-inline-archive-no-arg",
			args:    []string{"devtools", "pack-inline-archive"},
			wantMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:    "pack-inline-archive-not-a-dir",
			args:    []string{"devtools", "pack-inline-archive", "minimal.config.js"},
			wantMsg: "is not a directory",
		},
	}

	// A real file so the pack-inline-archive "not a directory" case reaches its
	// stat check rather than failing earlier.
	clitest.WriteMinimalConfig(p)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, tc.args...)
			if res.ExitCode == 0 {
				t.Fatalf("`%s` exit = 0, want non-zero\nstdout:\n%s",
					strings.Join(tc.args, " "), res.Stdout)
			}
			if !strings.Contains(res.Stderr, tc.wantMsg) {
				t.Errorf("`%s` stderr = %q, want to contain %q",
					strings.Join(tc.args, " "), res.Stderr, tc.wantMsg)
			}
			if strings.Contains(res.Stderr, "Usage:") || strings.Contains(res.Stdout, "Usage:") {
				t.Errorf("`%s` printed usage block (SilenceUsage broken):\nstdout:\n%s\nstderr:\n%s",
					strings.Join(tc.args, " "), res.Stdout, res.Stderr)
			}
		})
	}
}

// appsListConfigJS declares two shell apps (which download nothing) so
// `devtools apps list` produces a deterministic, fully offline listing. Neither
// is installed, so both report "not installed".
const appsListConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: {
    "hello-shell": { shell: { name: "echo" }, description: "say hi" },
    "ztool": { shell: { name: "true" } }
  },
  runtimes: {}, setup: {}, tools: {}
});
globalThis.getMinVersion = () => "0.0.0";
`

// bundlesListConfigJS declares two file-only bundles (no downloads) so
// `devtools bundles list` is deterministic and offline.
const bundlesListConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: {}, runtimes: {}, setup: {}, tools: {},
  bundles: {
    "alpha-bundle": { version: "1.0", files: { "a.txt": "hi" } },
    "beta-bundle": { files: { "b.txt": "yo" } }
  }
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestDevtoolsAppsList freezes `devtools apps list`: empty against the minimal
// config, and a sorted "name (type) [- desc] - not installed" listing against a
// two-shell-app config.
func TestDevtoolsAppsList(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "apps", "list")
		if res.ExitCode != 0 {
			t.Fatalf("`devtools apps list` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Errorf("`devtools apps list` against empty config should print nothing, got:\n%s", res.Stdout)
		}
	})

	t.Run("populated", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := p.WriteFile("apps.config.js", appsListConfigJS)
		norm := clitest.NewNormalizer()
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "apps", "list")
		if res.ExitCode != 0 {
			t.Fatalf("`devtools apps list` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		clitest.AssertGolden(t, "devtools_apps_list", norm.Apply(res.Stdout))
	})
}

// TestDevtoolsBundlesList freezes `devtools bundles list`: empty against the
// minimal config, and a sorted "name [(version)] - not installed" listing
// against a two-bundle config.
func TestDevtoolsBundlesList(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "bundles", "list")
		if res.ExitCode != 0 {
			t.Fatalf("`devtools bundles list` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Errorf("`devtools bundles list` against empty config should print nothing, got:\n%s", res.Stdout)
		}
	})

	t.Run("populated", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := p.WriteFile("bundles.config.js", bundlesListConfigJS)
		norm := clitest.NewNormalizer()
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "bundles", "list")
		if res.ExitCode != 0 {
			t.Fatalf("`devtools bundles list` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		clitest.AssertGolden(t, "devtools_bundles_list", norm.Apply(res.Stdout))
	})
}

// TestDevtoolsAppsInspectPath characterizes the offline error contract of
// `devtools apps inspect` and `devtools apps path`: an unknown app is a config
// error, and a known-but-not-installed app fails the install-root lookup. Both
// require local-only resolution — no network — and never print the usage block.
func TestDevtoolsAppsInspectPath(t *testing.T) {
	for _, sub := range []string{"inspect", "path"} {
		t.Run(sub+"-unknown", func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := clitest.WriteMinimalConfig(p)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, "devtools", "apps", sub, "nonesuch")
			assertOfflineError(t, res, "not found in config")
		})

		t.Run(sub+"-not-installed", func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("apps.config.js", appsListConfigJS)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, "devtools", "apps", sub, "hello-shell")
			assertOfflineError(t, res, "is not installed")
		})
	}
}

// TestDevtoolsBundlesInspectPath mirrors TestDevtoolsAppsInspectPath for the
// bundle inspection commands.
func TestDevtoolsBundlesInspectPath(t *testing.T) {
	for _, sub := range []string{"inspect", "path"} {
		t.Run(sub+"-unknown", func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := clitest.WriteMinimalConfig(p)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, "devtools", "bundles", sub, "nonesuch")
			assertOfflineError(t, res, "not found in config")
		})

		t.Run(sub+"-not-installed", func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("bundles.config.js", bundlesListConfigJS)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, "devtools", "bundles", sub, "alpha-bundle")
			assertOfflineError(t, res, "is not installed")
		})
	}
}

// assertOfflineError asserts a runtime error: non-zero exit, wantMsg present on
// stderr, and no usage block (SilenceUsage).
func assertOfflineError(t *testing.T, res clitest.Result, wantMsg string) {
	t.Helper()
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, wantMsg) {
		t.Errorf("stderr = %q, want to contain %q", res.Stderr, wantMsg)
	}
	if strings.Contains(res.Stderr, "Usage:") || strings.Contains(res.Stdout, "Usage:") {
		t.Errorf("runtime error printed usage block (SilenceUsage broken):\nstdout:\n%s\nstderr:\n%s",
			res.Stdout, res.Stderr)
	}
}
