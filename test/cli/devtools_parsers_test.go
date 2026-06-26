package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// expectedParsersSubcommands is the drift guard for the `devtools parsers` group.
var expectedParsersSubcommands = []string{"inspect", "list", "prefetch", "run"}

// devtoolsParsersHelpCases freezes the static help surfaces for the parsers group.
var devtoolsParsersHelpCases = []struct {
	name   string
	args   []string
	golden string
}{
	{"parsers", []string{"devtools", "parsers", "--help"}, "devtools_parsers_help"},
	{"list", []string{"devtools", "parsers", "list", "--help"}, "devtools_parsers_list_help"},
	{"inspect", []string{"devtools", "parsers", "inspect", "--help"}, "devtools_parsers_inspect_help"},
	{"run", []string{"devtools", "parsers", "run", "--help"}, "devtools_parsers_run_help"},
	{"prefetch", []string{"devtools", "parsers", "prefetch", "--help"}, "devtools_parsers_prefetch_help"},
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
// {inspect, list, prefetch, run}.
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

// TestDevtoolsParsersRun exercises the end-to-end debug path offline: pipe real
// eslint --format json output into `parsers run eslint --wasm <fixture>` and
// assert the structured diagnostics come back. The committed echo.wasm fixture
// bundles every parser, including eslint.
func TestDevtoolsParsersRun(t *testing.T) {
	wasm, err := os.ReadFile(filepath.Join("..", "..", "internal", "parsermanager", "testdata", "echo.wasm"))
	if err != nil {
		t.Fatalf("read wasm fixture: %v", err)
	}
	p := clitest.NewProject(t)
	wasmPath := filepath.Join(p.Dir, "parsers.wasm")
	if err := os.WriteFile(wasmPath, wasm, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	eslintJSON := `[{"filePath":"a.js","messages":[` +
		`{"ruleId":"semi","severity":1,"message":"Missing semicolon.","line":2,"column":3,"endLine":2,"endColumn":4}]}]`
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Stdin: eslintJSON},
		"devtools", "parsers", "run", "eslint", "--wasm", wasmPath, "--exit-code", "1")
	if res.ExitCode != 0 {
		t.Fatalf("`parsers run` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	for _, want := range []string{`"message": "Missing semicolon."`, `"code": "semi"`, `"row": 2`} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("`parsers run` output missing %q:\n%s", want, res.Stdout)
		}
	}
}

// TestDevtoolsParsersPrefetch freezes the offline contract: with no parsers
// declared, `prefetch` reports there is nothing to fetch and exits 0 (no network).
func TestDevtoolsParsersPrefetch(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "devtools", "parsers", "prefetch")
	if res.ExitCode != 0 {
		t.Fatalf("`devtools parsers prefetch` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no parsers declared") {
		t.Errorf("`devtools parsers prefetch` over empty config should note no parsers, got stderr:\n%s", res.Stderr)
	}
}
