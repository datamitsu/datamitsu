package cli_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// TestConfigLoadErrorPaths exercises the config-loading failure branches in
// cmd/config_loader.go that the offline golden suite does not reach: a missing
// file, an unreadable file, and configs that violate the getMinVersion()
// contract. Each must surface a clear error on stderr, exit non-zero, and never
// print the usage block (SilenceUsage). `config show` is the carrier command —
// it does nothing but load the config and print it, so the failure is purely in
// the loader.
func TestConfigLoadErrorPaths(t *testing.T) {
	p := clitest.NewProject(t)

	missing := p.Dir + "/does-not-exist.config.js"
	noMinVersion := p.WriteFile("no-min.config.js",
		"globalThis.getConfig = (c) => ({ apps: {}, runtimes: {}, setup: {}, tools: {} });\n")
	emptyMinVersion := p.WriteFile("empty-min.config.js",
		"globalThis.getConfig = (c) => ({});\nglobalThis.getMinVersion = () => \"\";\n")
	minVersionNotFunc := p.WriteFile("not-func-min.config.js",
		"globalThis.getConfig = (c) => ({});\nglobalThis.getMinVersion = 42;\n")

	cases := []struct {
		name    string
		path    string
		wantSub string
	}{
		{"missing-file", missing, "no such file or directory"},
		{"no-min-version", noMinVersion, "must export getMinVersion()"},
		{"empty-min-version", emptyMinVersion, "must return non-empty string"},
		{"min-version-not-func", minVersionNotFunc, "getMinVersion must be a function"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", tc.path, "config", "show")
			assertOfflineError(t, res, tc.wantSub)
			if !strings.Contains(res.Stderr, "failed to load config") {
				t.Errorf("stderr missing %q wrapper:\n%s", "failed to load config", res.Stderr)
			}
		})
	}
}

// TestConfigUnreadableFile covers the os.ReadFile permission-denied branch of
// the loader. Skipped when running as root (where 0000 files stay readable, so
// the branch cannot be provoked).
func TestConfigUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: 0000-mode files remain readable, cannot provoke permission denied")
	}

	p := clitest.NewProject(t)
	cfg := p.WriteFile("secret.config.js",
		"globalThis.getConfig = (c) => ({});\nglobalThis.getMinVersion = () => \"0.0.0\";\n")
	if err := os.Chmod(cfg, 0o000); err != nil {
		t.Fatalf("chmod 000: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfg, 0o644) }) // let TempDir cleanup remove it

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "show")
	assertOfflineError(t, res, "permission denied")
	if !strings.Contains(res.Stderr, "failed to read config file") {
		t.Errorf("stderr missing %q:\n%s", "failed to read config file", res.Stderr)
	}
}

// TestSubcommandUnknownFlag confirms an unknown flag attached to a leaf command
// (not the root) is rejected at parse time: exit non-zero, "unknown flag" on
// stderr, and — because flag errors set SilenceUsage — no usage block.
func TestSubcommandUnknownFlag(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "config", "show", "--definitely-not-a-flag")
	if res.ExitCode == 0 {
		t.Fatalf("unknown subcommand flag exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "unknown flag") {
		t.Errorf("stderr missing %q:\n%s", "unknown flag", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("flag error printed usage block (SilenceUsage broken):\n%s", res.Stderr)
	}
}

// TestExecListToolsConfigError covers the os.Exit(1) branch in exec.go reached
// when `exec` with no app name fails to load the config (the list-tools path,
// distinct from the unknown-app path). exec prints with its own "Error:" prefix
// and exits 1 directly; the coverage runtime's exit hook still flushes counters.
func TestExecListToolsConfigError(t *testing.T) {
	p := clitest.NewProject(t)
	missing := p.Dir + "/nope.config.js"

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", missing, "exec")
	if res.ExitCode != 1 {
		t.Fatalf("`exec` (bad config) exit = %d, want 1\nstdout:\n%s\nstderr:\n%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "Error:") || !strings.Contains(res.Stderr, "failed to load config") {
		t.Errorf("stderr missing exec error prefix/message:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") || strings.Contains(res.Stdout, "Usage:") {
		t.Errorf("runtime error printed usage block (SilenceUsage broken):\nstdout:\n%s\nstderr:\n%s",
			res.Stdout, res.Stderr)
	}
}

// TestInitReadOnlyTargetDir covers the write-failure branch of `init`: a real
// (non-dry-run) init in a read-only project root cannot create its .datamitsu
// scaffolding, so it must fail gracefully (exit 1, clear error, no usage), not
// panic. The config file is written before the directory is sealed, so it stays
// readable; a cleanup restores write permission so TempDir teardown can remove
// the tree.
func TestInitReadOnlyTargetDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory is still writable, cannot provoke the failure")
	}

	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	if err := os.Chmod(p.Dir, 0o555); err != nil {
		t.Fatalf("chmod 555 project dir: %v", err)
	}
	// Registered after TempDir's own cleanup (LIFO → this runs first), so the
	// directory is writable again before teardown tries to remove it.
	t.Cleanup(func() { _ = os.Chmod(p.Dir, 0o755) })

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "init")
	if res.ExitCode == 0 {
		t.Fatalf("read-only `init` exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "permission denied") {
		t.Errorf("stderr missing %q:\n%s", "permission denied", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error printed usage block (SilenceUsage broken):\n%s", res.Stderr)
	}
}

// TestConfigLockfileListAndErrorPaths exercises the early branches of
// `config lockfile` that the golden suite does not reach and that need no
// network: the no-argument listing path (empty config → "No apps with lock file
// support found."), an unknown app name, and a binary app (which has no
// dependency manifest, so lockfile generation is rejected before any install).
func TestConfigLockfileListAndErrorPaths(t *testing.T) {
	p := clitest.NewProject(t)

	t.Run("no-args-empty-config-lists", func(t *testing.T) {
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "lockfile")
		if res.ExitCode != 0 {
			t.Fatalf("`config lockfile` (no args) exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stderr, "No apps with lock file support found.") {
			t.Errorf("stderr missing empty-list message:\n%s", res.Stderr)
		}
	})

	t.Run("unknown-app", func(t *testing.T) {
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "lockfile", "ghost")
		assertOfflineError(t, res, `app "ghost" not found in configuration`)
	})

	t.Run("binary-app-unsupported", func(t *testing.T) {
		cfg := p.WriteFile("binary.config.js", lockfileBinaryConfigJS)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "config", "lockfile", "mytool")
		assertOfflineError(t, res, "does not support lock files")
	})
}

const lockfileBinaryConfigJS = `const H = "0000000000000000000000000000000000000000000000000000000000000000";
function mkBin() {
  const b = { binaries: {} };
  for (const os of ["linux", "darwin"]) {
    b.binaries[os] = {};
    for (const arch of ["amd64", "arm64"]) {
      b.binaries[os][arch] = {};
      for (const libc of ["glibc", "musl", "unknown"]) {
        b.binaries[os][arch][libc] = { url: "https://example.invalid/mytool", hash: H, contentType: "raw" };
      }
    }
  }
  return b;
}
globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({ apps: { "mytool": { binary: mkBin() } }, runtimes: {}, setup: {}, tools: {} });
globalThis.getMinVersion = () => "0.0.0";
`

// TestRuntimeConfigToleratesBadEnv characterizes the root-command initializer
// (cobra.OnInitialize → runtimeconfig.Init). Init reads env getters that fall
// back to their defaults on invalid input, so it never returns an error — which
// means the os.Exit(1) branch guarding it in root.go is, by design, unreachable
// from the CLI. A garbage DATAMITSU_INSTALL_TIMEOUT must therefore NOT abort the
// process; `config runtime` still succeeds and reports the default (600s).
func TestRuntimeConfigToleratesBadEnv(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{Env: []string{"DATAMITSU_INSTALL_TIMEOUT=notanumber"}},
		"config", "runtime")
	if res.ExitCode != 0 {
		t.Fatalf("`config runtime` with bad env exit = %d, want 0 (Init tolerates bad env)\nstderr:\n%s",
			res.ExitCode, res.Stderr)
	}

	var eff struct {
		InstallTimeoutSeconds int `json:"installTimeoutSeconds"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &eff); err != nil {
		t.Fatalf("config runtime JSON unmarshal: %v\nstdout:\n%s", err, res.Stdout)
	}
	if eff.InstallTimeoutSeconds != 600 {
		t.Errorf("installTimeoutSeconds = %d, want 600 (bad env falls back to default)",
			eff.InstallTimeoutSeconds)
	}
}
