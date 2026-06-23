package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// execToolsConfigJS is a minimal config declaring two shell apps. Shell apps
// download nothing (their command resolves on PATH), so `exec` can list and run
// them fully offline. "echo" is used so an actual run is deterministic and
// portable, and the apps are named out of sort order to prove the listing sorts.
const execToolsConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: {
    "hello-shell": { shell: { name: "echo" }, description: "say hi" },
    "ztool": { shell: { name: "true" } }
  },
  runtimes: {}, setup: {}, tools: {}
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestExecListEmpty freezes `exec` with no app name against the empty minimal
// config: the "Available tools:" header with no groups (exit 0). This is the
// degenerate listing — no apps of any type — and the contract that an empty
// registry is reported, not an error.
func TestExecListEmpty(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "exec")
	if res.ExitCode != 0 {
		t.Fatalf("`exec` (empty) exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`exec` (empty) wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "exec_list_empty", norm.Apply(res.Stdout))
}

// TestExecListGrouped freezes `exec` with no app name against a config with two
// shell apps: the "[shell]" group header, both apps sorted by name, and the
// optional detail column (command + description). Output is plain text with no
// paths/versions/durations, so the golden is fully stable offline.
func TestExecListGrouped(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", execToolsConfigJS)
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "exec")
	if res.ExitCode != 0 {
		t.Fatalf("`exec` (grouped) exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`exec` (grouped) wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "exec_list_grouped", norm.Apply(res.Stdout))
}

// TestExecUnknownApp locks the offline error contract for an app not in the
// registry: exit 1 with a clear "not found in registry" message on stderr, and
// no usage block (SilenceUsage — it is a runtime error, not a CLI misuse).
func TestExecUnknownApp(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "exec", "nonesuch")
	if res.ExitCode != 1 {
		t.Fatalf("`exec nonesuch` exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
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

// TestExecArgPassthrough proves that everything after `--` reaches the resolved
// tool verbatim. `exec hello-shell -- --flag value` runs the shell app (echo)
// with exactly `--flag value`, so the tool's stdout is the unmangled args. This
// is the contract the upcoming core rewrite must not break.
func TestExecArgPassthrough(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", execToolsConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "exec", "hello-shell", "--", "--flag", "value")
	if res.ExitCode != 0 {
		t.Fatalf("`exec hello-shell -- --flag value` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if got := res.Stdout; got != "--flag value\n" {
		t.Errorf("passthrough args were mangled: stdout = %q, want %q", got, "--flag value\n")
	}
}

// TestExecFlagsBeforeSeparatorRejected characterizes the converse: without the
// `--` separator, Cobra owns the flag parsing and an app-style flag that exec
// does not define is rejected before the tool runs. This documents why callers
// must use `--` for passthrough.
func TestExecFlagsBeforeSeparatorRejected(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", execToolsConfigJS)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "exec", "hello-shell", "--flag", "value")
	if res.ExitCode == 0 {
		t.Fatalf("`exec hello-shell --flag value` (no separator) exit = 0, want non-zero\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "unknown flag") {
		t.Errorf("stderr should report the unknown flag:\n%s", res.Stderr)
	}
}
