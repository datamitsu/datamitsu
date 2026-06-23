package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// installBinaryConfigJS declares a single binary app ("mytool") for every
// os/arch/libc combination, so the offline-download path is exercised on any
// host platform (the binary candidate is always found; only the network fetch
// fails). The URL is unreachable and offline mode blocks it before any request,
// so this stays fully hermetic.
const installBinaryConfigJS = `const H = "0000000000000000000000000000000000000000000000000000000000000000";
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

// TestInstallNoTargets locks the contract that `install` with neither an app
// name nor a --runtime is a usage-independent runtime error: exit 1 with a clear
// message and no usage block (SilenceUsage). Note the message names what to
// provide ("specify at least one app name or --runtime <name>"), not the
// plan-era phrasing "nothing to install" — this characterizes the real output.
func TestInstallNoTargets(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "install")
	if res.ExitCode != 1 {
		t.Fatalf("`install` (no targets) exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "specify at least one app name or --runtime") {
		t.Errorf("stderr should explain what to provide:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
}

// TestInstallHelpGolden freezes `install --help`: a static, offline help block
// with no version or path tokens, so the normalized output equals the raw
// output. The --runtime and --no-verify flags appearing here is itself part of
// the frozen contract.
func TestInstallHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "install", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`install --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`install --help` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "install_help", norm.Apply(res.Stdout))
}

// TestInstallUnknownApp locks the offline error for an app not in the registry:
// exit 1, "not found in registry" naming the app, no usage block.
func TestInstallUnknownApp(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "install", "nonesuch")
	if res.ExitCode != 1 {
		t.Fatalf("`install nonesuch` exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not found in registry") {
		t.Errorf("stderr should report the unknown app:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "nonesuch") {
		t.Errorf("stderr should name the requested app:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
}

// TestInstallOfflineDownloadError exercises the real install path for a declared
// binary app and characterizes the graceful offline failure: the download is
// blocked by DATAMITSU_OFFLINE before any network request, surfacing a non-zero
// exit and an actionable message (mentions offline mode and how to recover).
// This also proves `install <app>` arg parsing reaches the resolver.
func TestInstallOfflineDownloadError(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("bin.config.js", installBinaryConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "install", "mytool")
	if res.ExitCode != 1 {
		t.Fatalf("`install mytool` (offline) exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "offline mode") {
		t.Errorf("stderr should report the offline block:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "DATAMITSU_OFFLINE") {
		t.Errorf("stderr should name the offline env var so it is actionable:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
}

// TestInstallNoVerifyFlagParses proves --no-verify is accepted and routes
// through the same install path (still a graceful offline error for the binary
// app). The flag's only effect is skipping the post-install version check, which
// is never reached offline, so the observable contract is identical to the
// verify-on case above — what matters here is that the flag parses.
func TestInstallNoVerifyFlagParses(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("bin.config.js", installBinaryConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "install", "--no-verify", "mytool")
	if res.ExitCode != 1 {
		t.Fatalf("`install --no-verify mytool` exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "offline mode") {
		t.Errorf("stderr should report the offline block:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stderr, "Usage:") {
		t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
	}
}

// TestInstallRuntimeFlagParses proves --runtime is accepted (repeatable string
// slice) and that requesting a runtime not declared by the config is a no-op
// success offline: nothing to download, exit 0. This characterizes the parse +
// no-op contract without needing the network.
func TestInstallRuntimeFlagParses(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "install", "--runtime", "node")
	if res.ExitCode != 0 {
		t.Fatalf("`install --runtime node` (undeclared) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}
