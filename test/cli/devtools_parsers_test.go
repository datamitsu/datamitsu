package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// expectedParsersSubcommands is the drift guard for the `devtools parsers` group.
var expectedParsersSubcommands = []string{"inspect", "list"}

// devtoolsParsersHelpCases freezes the static help surfaces for the parsers group.
var devtoolsParsersHelpCases = []struct {
	name   string
	args   []string
	golden string
}{
	{"parsers", []string{"devtools", "parsers", "--help"}, "devtools_parsers_help"},
	{"list", []string{"devtools", "parsers", "list", "--help"}, "devtools_parsers_list_help"},
	{"inspect", []string{"devtools", "parsers", "inspect", "--help"}, "devtools_parsers_inspect_help"},
}

func TestDevtoolsParsersHelpGolden(t *testing.T) {
	for _, tc := range devtoolsParsersHelpCases {
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

// TestDevtoolsParsersCommandSetDrift asserts the parsers subcommand set is exactly
// {inspect, list}.
func TestDevtoolsParsersCommandSetDrift(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "devtools", "parsers", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`devtools parsers --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	got := parseAvailableCommands(res.Stdout)
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(expectedParsersSubcommands), ",") {
		t.Errorf("parsers subcommand set drift:\n got: %v\nwant: %v", got, expectedParsersSubcommands)
	}
}

// TestDevtoolsParsersList freezes `devtools parsers list` against an empty config:
// no parsers declared → nothing listed, fully offline (no module download).
func TestDevtoolsParsersList(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "devtools", "parsers", "list")
	if res.ExitCode != 0 {
		t.Fatalf("`devtools parsers list` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "" {
		t.Errorf("`devtools parsers list` against empty config should print nothing, got:\n%s", res.Stdout)
	}
}

// TestDevtoolsParsersListJSONEmpty locks the machine-readable contract: with no
// parsers the JSON is a stable empty catalog (`tools` is [] not null), so build
// pipelines can rely on the shape.
func TestDevtoolsParsersListJSONEmpty(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "devtools", "parsers", "list", "--json")
	if res.ExitCode != 0 {
		t.Fatalf("`devtools parsers list --json` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, `"tools": []`) {
		t.Errorf("empty --json catalog should contain `\"tools\": []`, got:\n%s", res.Stdout)
	}
}

// TestDevtoolsParsersInspect characterizes the offline error contract: inspecting
// a tool no configured parser provides fails with a descriptive message and no
// usage block.
func TestDevtoolsParsersInspect(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "devtools", "parsers", "inspect", "ghost")
	assertOfflineError(t, res, "not provided by any configured parser")
}
