package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// setupToolsConfigJS is a synthetic config with two tools (and no apps/setup
// entries) used to exercise setup's tool-scoping and opt-in-ignore behavior
// offline. The tools own no setup config, so `--tools` scoping surfaces a
// "no config generated" notice and `--opt-in-tools` lists exactly these two
// names — both deterministic.
const setupToolsConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: {}, runtimes: {}, setup: {},
  tools: {
    "alpha": { name: "alpha", operations: { lint: { app: "alpha-bin", args: ["x"], scope: "repository" } } },
    "beta":  { name: "beta",  operations: { lint: { app: "beta-bin",  args: ["y"], scope: "repository" } } }
  }
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestSetupHelpGolden freezes `setup --help`: a static, offline help block with
// no version or path tokens, so the normalized output equals the raw output.
// setup has no subcommands, so there is no subcommand-set drift guard.
func TestSetupHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "setup", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`setup --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`setup --help` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "setup_help", norm.Apply(res.Stdout))
}

// TestSetupDryRunGolden freezes `setup --dry-run` against the minimal config:
// the banner, the single "setup" frame with "no project types detected", the
// dry-run notice, and the "dry-run" footer. The build version and the run
// duration are masked, so the golden is stable across machines and builds.
// Dry-run makes no network calls and writes nothing, so it is fully offline.
func TestSetupDryRunGolden(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "setup", "--dry-run")
	if res.ExitCode != 0 {
		t.Fatalf("`setup --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`setup --dry-run` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "setup_dry_run", norm.Apply(res.Stdout))
}

// TestSetupOptInToolsDryRun freezes `setup --opt-in-tools --dry-run` against a
// config with two tools: the plan reports the all-disabled .datamitsuignore it
// would write (listing the two tools' count) without touching the filesystem.
// The actual ignore file must NOT exist after a dry run.
func TestSetupOptInToolsDryRun(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", setupToolsConfigJS)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "setup", "--opt-in-tools", "--dry-run")
	if res.ExitCode != 0 {
		t.Fatalf("`setup --opt-in-tools --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`setup --opt-in-tools --dry-run` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "setup_opt_in_tools", norm.Apply(res.Stdout))

	if _, err := os.Stat(filepath.Join(p.Dir, ".datamitsuignore")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write .datamitsuignore (stat err = %v)", err)
	}
}

// TestSetupToolsScopedDryRun freezes `setup --tools alpha --dry-run`: because the
// synthetic tools own no setup config, scoping to alpha surfaces the
// "no config generated for alpha" notice — characterizing the tool-scoped no-op.
func TestSetupToolsScopedDryRun(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", setupToolsConfigJS)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "setup", "--tools", "alpha", "--dry-run")
	if res.ExitCode != 0 {
		t.Fatalf("`setup --tools alpha --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`setup --tools alpha --dry-run` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "setup_tools_scoped", norm.Apply(res.Stdout))
}

// TestSetupFlagCombosOffline proves the remaining flags parse and behave offline
// in dry-run mode against the minimal config. --skip-fix only affects the
// post-setup fix (never reached in dry-run) and --no-verify-hash only relaxes a
// gate that has nothing to check (no pins), so both yield output byte-identical
// to a baseline `setup --dry-run`.
func TestSetupFlagCombosOffline(t *testing.T) {
	norm := clitest.NewNormalizer()

	base := clitest.NewProject(t)
	baseCfg := clitest.WriteMinimalConfig(base)
	baseRes := clitest.Run(t, clitest.RunOptions{Dir: base.Dir},
		"--no-auto-config", "--config", baseCfg, "setup", "--dry-run")
	if baseRes.ExitCode != 0 {
		t.Fatalf("baseline `setup --dry-run` exit = %d, want 0\nstderr:\n%s", baseRes.ExitCode, baseRes.Stderr)
	}
	want := norm.Apply(baseRes.Stdout)

	cases := []struct {
		name string
		flag string
	}{
		{"skip-fix", "--skip-fix"},
		{"no-verify-hash", "--no-verify-hash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := clitest.WriteMinimalConfig(p)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, "setup", "--dry-run", tc.flag)
			if res.ExitCode != 0 {
				t.Fatalf("`setup --dry-run %s` exit = %d, want 0\nstderr:\n%s", tc.flag, res.ExitCode, res.Stderr)
			}
			if got := norm.Apply(res.Stdout); got != want {
				t.Errorf("`setup --dry-run %s` output differs from baseline:\n got: %q\nwant: %q", tc.flag, got, want)
			}
		})
	}
}

// TestSetupUnknownTools locks the offline `--tools` validation error: an unknown
// tool name exits non-zero, names the missing tool and the available set, and
// prints no usage block (SilenceUsage).
func TestSetupUnknownTools(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", setupToolsConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "setup", "--tools", "nonesuch", "--dry-run")
	if res.ExitCode == 0 {
		t.Fatalf("`setup --tools nonesuch` exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "tools not found") || !strings.Contains(res.Stderr, "nonesuch") {
		t.Errorf("stderr should name the missing tool:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
}

// chainDriftBeforeConfigJS generates a setup file ("drift.txt") whose upstream
// (before-config) content is fixed; the root config below pins a deliberately
// wrong expectChainHash, so the chain-hash gate must fail.
const chainDriftBeforeConfigJS = `globalThis.getConfig = (config) => ({
  apps: {}, runtimes: {}, tools: {},
  setup: { "drift.txt": { content: () => "upstream\n" } },
});
globalThis.getMinVersion = () => "0.0.0";
`

// chainDriftRootConfigJS inherits the before-config and pins a wrong
// expectChainHash on the same setup file, triggering a drift report.
const chainDriftRootConfigJS = `globalThis.getBeforeConfigs = () => [{ path: "before.config.js" }];
globalThis.getConfig = (config) => ({
  apps: {}, runtimes: {}, tools: {},
  setup: { "drift.txt": { content: () => "root\n", expectChainHash: "xxh3:00000000000000000000000000000000" } },
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestSetupChainHashDrift locks the chain-hash gate: when a pinned config drifted
// from its upstream chain and --no-verify-hash is omitted, setup aborts before any
// filesystem write, reporting the mismatch (expected/actual + the incoming content)
// without printing a usage block. The target file must not be created. Passing
// --no-verify-hash bypasses the gate and the run succeeds. Uses auto-discovery so
// getBeforeConfigs() is honored (no --no-auto-config / --config).
func TestSetupChainHashDrift(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("before.config.js", chainDriftBeforeConfigJS)
	p.WriteFile("datamitsu.config.js", chainDriftRootConfigJS)

	// Real (non-dry-run) run so "without writing" is a meaningful assertion: the
	// gate must abort before the file is ever produced.
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "setup")
	if res.ExitCode == 0 {
		t.Fatalf("`setup` with chain-hash drift exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "chain-hash verification failed") {
		t.Errorf("stderr should report the chain-hash failure:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "drift.txt") {
		t.Errorf("stderr should name the drifted file:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "drift.txt")); !os.IsNotExist(err) {
		t.Errorf("drift gate must not write drift.txt (stat err = %v)", err)
	}

	// --no-verify-hash bypasses the gate: the drifted file is written (here a
	// dry-run so we only assert the gate no longer blocks).
	bypass := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "setup", "--dry-run", "--no-verify-hash")
	if bypass.ExitCode != 0 {
		t.Fatalf("`setup --dry-run --no-verify-hash` exit = %d, want 0\nstderr:\n%s", bypass.ExitCode, bypass.Stderr)
	}
	if strings.Contains(bypass.Stderr, "chain-hash verification failed") {
		t.Errorf("--no-verify-hash should bypass the gate, but it still reported drift:\n%s", bypass.Stderr)
	}
}
