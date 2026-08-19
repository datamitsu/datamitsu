package cli_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// expectedTopLevelCommands is the exact set of top-level commands datamitsu
// exposes (cobra's built-in `completion` and `help` included). This is a drift
// guard: a new command must be added here deliberately, with its own blackbox
// test, rather than silently slipping into the contract untested.
var expectedTopLevelCommands = []string{
	"cache",
	"check",
	"completion",
	"config",
	"devtools",
	"exec",
	"fix",
	"help",
	"init",
	"install",
	"lint",
	"llms",
	"lsp",
	"setup",
	"source",
	"store",
	"version",
}

// TestRootHelpGolden freezes the root help text. `--help` and the no-args
// invocation both print the same help block (cobra prints usage when no
// subcommand runs), so we golden one and assert the other is byte-identical.
func TestRootHelpGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	help := clitest.Run(t, clitest.RunOptions{}, "--help")
	if help.ExitCode != 0 {
		t.Fatalf("`--help` exit = %d, want 0\nstderr:\n%s", help.ExitCode, help.Stderr)
	}
	if help.Stderr != "" {
		t.Errorf("`--help` wrote to stderr:\n%s", help.Stderr)
	}
	clitest.AssertGolden(t, "root_help", norm.Apply(help.Stdout))

	noArgs := clitest.Run(t, clitest.RunOptions{})
	if noArgs.ExitCode != 0 {
		t.Fatalf("no-args exit = %d, want 0\nstderr:\n%s", noArgs.ExitCode, noArgs.Stderr)
	}
	if got, want := norm.Apply(noArgs.Stdout), norm.Apply(help.Stdout); got != want {
		t.Errorf("no-args help differs from `--help`:\n%s", got)
	}
}

// TestHelpSubcommandGolden freezes `help <cmd>` (the explicit help path, which
// differs from `<cmd> --help` only cosmetically). version is a stable, offline
// leaf command with no version string in its help text.
func TestHelpSubcommandGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "help", "version")
	if res.ExitCode != 0 {
		t.Fatalf("`help version` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "help_version", norm.Apply(res.Stdout))
}

// TestRootCommandListDrift asserts the set of top-level commands shown in
// `--help` is exactly expectedTopLevelCommands, decoupled from help formatting:
// it parses the "Available Commands:" block and compares the name set.
func TestRootCommandListDrift(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`--help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	got := parseAvailableCommands(res.Stdout)
	want := append([]string(nil), expectedTopLevelCommands...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("top-level command set drift:\n got: %v\nwant: %v", got, want)
	}
}

// TestRootGlobalFlagsAccepted runs `version` with each persistent flag to prove
// the root command accepts the documented global flags without error. version
// is a pure, offline no-op so only flag parsing is under test.
func TestRootGlobalFlagsAccepted(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	cases := []struct {
		name string
		args []string
	}{
		{"verbose-long", []string{"--verbose", "version"}},
		{"verbose-short", []string{"-v", "version"}},
		{"no-auto-config", []string{"--no-auto-config", "version"}},
		{"no-oci", []string{"--no-oci", "version"}},
		{"config", []string{"--no-auto-config", "--config", cfg, "version"}},
		{"config-repeated", []string{"--no-auto-config", "--config", cfg, "--config", cfg, "version"}},
		{"before-config", []string{"--before-config", cfg, "version"}},
		{"binary-command", []string{"--binary-command", "dm", "version"}},
		{"all-combined", []string{
			"--verbose", "--no-auto-config", "--no-oci",
			"--config", cfg, "--before-config", cfg, "--binary-command", "dm", "version",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, tc.args...)
			if res.ExitCode != 0 {
				t.Fatalf("`%s` exit = %d, want 0\nstderr:\n%s",
					strings.Join(tc.args, " "), res.ExitCode, res.Stderr)
			}
			if !strings.Contains(res.Stdout, "version") {
				t.Errorf("expected version output, got stdout:\n%s", res.Stdout)
			}
		})
	}
}

// TestRootSilenceUsage proves SilenceUsage/SilenceErrors: a runtime error (after
// successful flag parsing) must NOT dump the usage block. `exec <unknown>`
// resolves an app that does not exist and fails at runtime, fully offline.
func TestRootSilenceUsage(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "exec", "definitely-not-an-app")
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unknown app, got 0\nstdout:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stderr, "Usage:") || strings.Contains(res.Stdout, "Usage:") {
		t.Errorf("runtime error printed usage block (SilenceUsage broken):\nstdout:\n%s\nstderr:\n%s",
			res.Stdout, res.Stderr)
	}
	if strings.TrimSpace(res.Stderr) == "" {
		t.Errorf("expected an error message on stderr, got none")
	}
}

// TestRootBadFlags covers flag-parsing error paths: unknown flags, missing flag
// arguments, bad value types, and unknown commands. Each must exit non-zero and
// report the problem; flag-parse errors are reported on stderr and never print
// usage (SilenceUsage).
func TestRootBadFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantSub string // substring expected somewhere in stderr
	}{
		{"unknown-global-flag", []string{"--definitely-not-a-flag", "version"}, "unknown flag"},
		{"config-missing-arg", []string{"--config"}, "flag needs an argument"},
		{"verbose-bad-bool", []string{"--verbose=maybe", "version"}, "invalid argument"},
		{"unknown-command", []string{"definitely-not-a-command"}, "unknown command"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{}, tc.args...)
			if res.ExitCode == 0 {
				t.Fatalf("`%s` exit = 0, want non-zero\nstdout:\n%s",
					strings.Join(tc.args, " "), res.Stdout)
			}
			if !strings.Contains(res.Stderr, tc.wantSub) {
				t.Errorf("stderr missing %q:\n%s", tc.wantSub, res.Stderr)
			}
			if strings.Contains(res.Stderr, "Usage:") {
				t.Errorf("flag error printed usage block (SilenceUsage broken):\n%s", res.Stderr)
			}
		})
	}
}

// parseAvailableCommands extracts the command names from the "Available
// Commands:" section of cobra help output. Each entry is "  name  Short desc";
// the section ends at the first blank line.
func parseAvailableCommands(help string) []string {
	var names []string
	inSection := false
	for line := range strings.SplitSeq(help, "\n") {
		if strings.TrimSpace(line) == "Available Commands:" {
			inSection = true
			continue
		}
		if inSection {
			if strings.TrimSpace(line) == "" {
				break
			}
			fields := strings.Fields(line)
			if len(fields) > 0 {
				names = append(names, fields[0])
			}
		}
	}
	return names
}
