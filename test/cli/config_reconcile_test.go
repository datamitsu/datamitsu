package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// reconcileToolsConfigJS is a synthetic config with two tools and no managed
// config entries. It exercises tool scoping and opt-in-ignore behavior offline.
const reconcileToolsConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: {}, runtimes: {}, managedConfigs: {},
  tools: {
    "alpha": { name: "alpha", operations: { lint: { app: "alpha-bin", args: ["x"], scope: "repository" } } },
    "beta":  { name: "beta",  operations: { lint: { app: "beta-bin",  args: ["y"], scope: "repository" } } }
  }
});
globalThis.getMinVersion = () => "0.0.0";
`

func TestConfigReconcileHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "config", "reconcile", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`config reconcile --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`config reconcile --help` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "config_reconcile_help", norm.Apply(res.Stdout))
}

// Dry-run prints the reconciliation plan without writing managed files or
// running the post-reconciliation fix.
func TestConfigReconcileDryRunGolden(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--dry-run")
	if res.ExitCode != 0 {
		t.Fatalf("`config reconcile --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`config reconcile --dry-run` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "config_reconcile_dry_run", norm.Apply(res.Stdout))
}

func TestConfigReconcileOptInToolsDryRun(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", reconcileToolsConfigJS)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--opt-in-tools", "--dry-run")
	if res.ExitCode != 0 {
		t.Fatalf("`config reconcile --opt-in-tools --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`config reconcile --opt-in-tools --dry-run` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "config_reconcile_opt_in_tools_dry_run", norm.Apply(res.Stdout))

	if _, err := os.Stat(filepath.Join(p.Dir, ".datamitsuignore")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write .datamitsuignore (stat err = %v)", err)
	}
}

func TestConfigReconcileToolsScopedDryRun(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", reconcileToolsConfigJS)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--tools", "alpha", "--dry-run")
	if res.ExitCode != 0 {
		t.Fatalf("`config reconcile --tools alpha --dry-run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`config reconcile --tools alpha --dry-run` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "config_reconcile_tools_scoped_dry_run", norm.Apply(res.Stdout))
}

func TestConfigReconcileNoVerifyHashParsesOffline(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--dry-run", "--no-verify-hash")
	if res.ExitCode != 0 {
		t.Fatalf("`config reconcile --no-verify-hash` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
}

func TestConfigReconcileUnknownTools(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", reconcileToolsConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "reconcile", "--tools", "nonesuch", "--dry-run")
	if res.ExitCode == 0 {
		t.Fatalf("`config reconcile --tools nonesuch` exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "tools not found") || !strings.Contains(res.Stderr, "nonesuch") {
		t.Errorf("stderr should name the missing tool:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
}

const reconcileWritesConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: {
    "fix-recorder": { shell: { name: "sh", args: ["-c", "printf fixed > fix-ran.txt"] } },
  },
  runtimes: {},
  projectTypes: { fixture: { markers: ["fixture.marker"] } },
  tools: {
    recorder: {
      name: "recorder",
      projectTypes: ["fixture"],
      operations: { fix: { app: "fix-recorder", args: [], scope: "repository" } },
    },
  },
  managedConfigs: { "generated.txt": { scope: "git-root", content: () => "managed\n" } },
});
globalThis.getMinVersion = () => "0.0.0";
`

func TestConfigReconcileWriteAndFixModes(t *testing.T) {
	assertManagedFile := func(t *testing.T, target string) {
		t.Helper()
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile(generated.txt): %v", err)
		}
		if string(got) != "managed\n" {
			t.Errorf("generated.txt = %q, want %q", got, "managed\\n")
		}
	}

	t.Run("default writes and runs fix", func(t *testing.T) {
		p := clitest.NewProject(t)
		p.WriteFile("fixture.marker", "fixture\n")
		cfg := p.WriteFile("reconcile.config.js", reconcileWritesConfigJS)
		target := filepath.Join(p.Dir, "generated.txt")
		fixMarker := filepath.Join(p.Dir, "fix-ran.txt")

		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "reconcile")
		if res.ExitCode != 0 {
			t.Fatalf("reconcile exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		assertManagedFile(t, target)
		got, err := os.ReadFile(fixMarker)
		if err != nil {
			t.Fatalf("default reconciliation did not run the fix operation: %v\nstdout:\n%s", err, res.Stdout)
		}
		if string(got) != "fixed" {
			t.Errorf("fix-ran.txt = %q, want %q", got, "fixed")
		}
	})

	t.Run("skip-fix writes without running fix", func(t *testing.T) {
		p := clitest.NewProject(t)
		p.WriteFile("fixture.marker", "fixture\n")
		cfg := p.WriteFile("reconcile.config.js", reconcileWritesConfigJS)
		target := filepath.Join(p.Dir, "generated.txt")
		fixMarker := filepath.Join(p.Dir, "fix-ran.txt")

		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "reconcile", "--skip-fix")
		if res.ExitCode != 0 {
			t.Fatalf("reconcile --skip-fix exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		assertManagedFile(t, target)
		if _, err := os.Stat(fixMarker); !os.IsNotExist(err) {
			t.Fatalf("--skip-fix ran the fix operation (stat err = %v)\nstdout:\n%s", err, res.Stdout)
		}
	})

	t.Run("dry-run writes nothing and skips fix", func(t *testing.T) {
		p := clitest.NewProject(t)
		p.WriteFile("fixture.marker", "fixture\n")
		cfg := p.WriteFile("reconcile.config.js", reconcileWritesConfigJS)
		target := filepath.Join(p.Dir, "generated.txt")
		fixMarker := filepath.Join(p.Dir, "fix-ran.txt")

		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "reconcile", "--dry-run")
		if res.ExitCode != 0 {
			t.Fatalf("reconcile --dry-run exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote generated.txt (stat err = %v)", err)
		}
		if _, err := os.Stat(fixMarker); !os.IsNotExist(err) {
			t.Fatalf("--dry-run ran the fix operation (stat err = %v)\nstdout:\n%s", err, res.Stdout)
		}
	})
}

func TestRemovedSetupCommandCannotWrite(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", reconcileWritesConfigJS)
	target := filepath.Join(p.Dir, "generated.txt")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "setup")
	if res.ExitCode == 0 {
		t.Fatalf("removed `setup` command exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("removed command wrote generated.txt (stat err = %v)", err)
	}
}

const chainDriftBeforeConfigJS = `globalThis.getConfig = () => ({
  apps: {}, runtimes: {}, tools: {},
  managedConfigs: { "drift.txt": { content: () => "upstream\n" } },
});
globalThis.getMinVersion = () => "0.0.0";
`

const chainDriftRootConfigJS = `globalThis.getBeforeConfigs = () => [{ path: "before.config.js" }];
globalThis.getConfig = () => ({
  apps: {}, runtimes: {}, tools: {},
  managedConfigs: { "drift.txt": { content: () => "root\n", expectChainHash: "xxh3:00000000000000000000000000000000" } },
});
globalThis.getMinVersion = () => "0.0.0";
`

func TestConfigReconcileChainHashDrift(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("before.config.js", chainDriftBeforeConfigJS)
	p.WriteFile("datamitsu.config.js", chainDriftRootConfigJS)
	target := filepath.Join(p.Dir, "drift.txt")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "config", "reconcile")
	if res.ExitCode == 0 {
		t.Fatalf("reconcile with chain-hash drift exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "chain-hash verification failed") || !strings.Contains(res.Stderr, "drift.txt") {
		t.Errorf("stderr should report the drifted file:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("drift gate must not write drift.txt (stat err = %v)", err)
	}

	bypass := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"config", "reconcile", "--skip-fix", "--no-verify-hash")
	if bypass.ExitCode != 0 {
		t.Fatalf("reconcile bypass exit = %d, want 0\nstderr:\n%s", bypass.ExitCode, bypass.Stderr)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("--no-verify-hash should allow reconciliation to write drift.txt: %v", err)
	}
}
