package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// expectedToolsSubcommands is the drift guard for the `devtools tools` group.
var expectedToolsSubcommands = []string{"inspect", "list"}

var devtoolsToolsHelpCases = []struct {
	name   string
	args   []string
	golden string
}{
	{"tools", []string{"devtools", "tools", "--help"}, "devtools_tools_help"},
	{"list", []string{"devtools", "tools", "list", "--help"}, "devtools_tools_list_help"},
	{"inspect", []string{"devtools", "tools", "inspect", "--help"}, "devtools_tools_inspect_help"},
}

func TestDevtoolsToolsHelpGolden(t *testing.T) {
	for _, tc := range devtoolsToolsHelpCases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{}, tc.args...)
			if res.ExitCode != 0 {
				t.Fatalf("`%s` exit = %d, want 0\nstderr:\n%s", strings.Join(tc.args, " "), res.ExitCode, res.Stderr)
			}
			if res.Stderr != "" {
				t.Errorf("`%s` wrote to stderr:\n%s", strings.Join(tc.args, " "), res.Stderr)
			}
			clitest.AssertGolden(t, tc.golden, res.Stdout)
		})
	}
}

func TestDevtoolsToolsCommandSetDrift(t *testing.T) {
	res := clitest.Run(t, clitest.RunOptions{}, "devtools", "tools", "--help")
	if res.ExitCode != 0 {
		t.Fatalf("`devtools tools --help` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	got := parseAvailableCommands(res.Stdout)
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(expectedToolsSubcommands), ",") {
		t.Errorf("tools subcommand set drift:\n got: %v\nwant: %v", got, expectedToolsSubcommands)
	}
}

// toolsListConfigJS declares two tools (apps are trivial shells; tools list never
// runs them) so `devtools tools list` is deterministic and offline.
const toolsListConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: { "a": { shell: { name: "true" } }, "b": { shell: { name: "true" } } },
  runtimes: {}, setup: {}, bundles: {},
  tools: {
    "alpha": { name: "Alpha", projectTypes: ["x"], operations: { lint: { app: "a", args: ["{file}"], scope: "per-file" } } },
    "beta": { name: "Beta", skip: true, skipReason: "demo", operations: { fix: { app: "b", args: [], scope: "repository" }, lint: { app: "b", args: [], scope: "repository" } } }
  }
});
globalThis.getMinVersion = () => "0.0.0";
`

func TestDevtoolsToolsList(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "tools", "list")
		if res.ExitCode != 0 {
			t.Fatalf("`devtools tools list` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Errorf("empty config should list no tools, got:\n%s", res.Stdout)
		}
	})

	t.Run("populated", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := p.WriteFile("tools.config.js", toolsListConfigJS)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "tools", "list")
		if res.ExitCode != 0 {
			t.Fatalf("`devtools tools list` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		clitest.AssertGolden(t, "devtools_tools_list", clitest.NewNormalizer().Apply(res.Stdout))
	})
}

func TestDevtoolsToolsInspect(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := clitest.WriteMinimalConfig(p)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "tools", "inspect", "nonesuch")
		assertOfflineError(t, res, "is not declared in config")
	})

	t.Run("known", func(t *testing.T) {
		p := clitest.NewProject(t)
		cfg := p.WriteFile("tools.config.js", toolsListConfigJS)
		res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
			"--no-auto-config", "--config", cfg, "devtools", "tools", "inspect", "alpha")
		if res.ExitCode != 0 {
			t.Fatalf("`devtools tools inspect alpha` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
		}
		for _, want := range []string{"alpha", "operations:", "app=a", "scope=per-file"} {
			if !strings.Contains(res.Stdout, want) {
				t.Errorf("inspect output missing %q:\n%s", want, res.Stdout)
			}
		}
	})
}
