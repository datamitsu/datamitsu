package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// explainToolsConfigJS is a small synthetic tool config used to freeze the
// --explain planning contract of check/fix/lint. Both tools are repository-scoped
// with no globs, so planning is deterministic and produces no per-file paths (only
// the masked working dir). "alpha" has both fix and lint operations (so it appears
// for all three commands); "beta" is lint-only (so the fix and lint plans differ).
// The referenced apps (alpha-bin/beta-bin) are not declared as real apps, which
// BinaryAvailable treats as available — so nothing is skipped and nothing downloads.
// --explain returns before any install step, so this is fully offline+deterministic.
const explainToolsConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  apps: {}, runtimes: {}, setup: {},
  tools: {
    "alpha": {
      name: "alpha",
      operations: {
        fix:  { app: "alpha-bin", args: ["--write"], scope: "repository" },
        lint: { app: "alpha-bin", args: ["--check"], scope: "repository" }
      }
    },
    "beta": {
      name: "beta",
      operations: {
        lint: { app: "beta-bin", args: ["lint"], scope: "repository" }
      }
    }
  }
});
globalThis.getMinVersion = () => "0.0.0";
`

// planShape is a partial view of the --explain=json contract, enough to assert the
// machine-readable shape (operation name, group/task structure) without pinning the
// machine-dependent working-dir paths.
type planShape struct {
	Operation string `json:"operation"`
	RootPath  string `json:"rootPath"`
	Groups    []struct {
		Priority       int `json:"priority"`
		ParallelGroups []struct {
			CanRunInParallel bool `json:"canRunInParallel"`
			Tasks            []struct {
				ToolName string   `json:"toolName"`
				App      string   `json:"app"`
				Args     []string `json:"args"`
				Scope    string   `json:"scope"`
			} `json:"tasks"`
		} `json:"parallelGroups"`
	} `json:"groups"`
	Skipped []any `json:"skipped"`
}

// TestCheckFixLintHelpGolden freezes `--help` for check, fix and lint. Each help
// block is static, offline text with no version or path tokens, so the normalized
// output equals the raw output. The shared --explain documentation block is also
// asserted present in each.
func TestCheckFixLintHelpGolden(t *testing.T) {
	for _, cmd := range []string{"check", "fix", "lint"} {
		t.Run(cmd, func(t *testing.T) {
			norm := clitest.NewNormalizer()
			res := clitest.Run(t, clitest.RunOptions{}, cmd, "--help")
			if res.ExitCode != 0 {
				t.Fatalf("`%s --help` exit = %d, want 0\nstderr:\n%s", cmd, res.ExitCode, res.Stderr)
			}
			if res.Stderr != "" {
				t.Errorf("`%s --help` wrote to stderr:\n%s", cmd, res.Stderr)
			}
			if !strings.Contains(res.Stdout, "--explain") {
				t.Errorf("`%s --help` should document --explain:\n%s", cmd, res.Stdout)
			}
			clitest.AssertGolden(t, cmd+"_help", norm.Apply(res.Stdout))
		})
	}
}

// runExplain runs `<cmd> --explain=<mode>` against the synthetic config in a fresh
// project, asserting exit 0 and empty stderr, and returns the normalized stdout
// (the project dir masked to <TMP>).
func runExplain(t *testing.T, cmd, mode string) string {
	t.Helper()
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", explainToolsConfigJS)
	norm := clitest.NewNormalizer().MaskPath(p.Dir, "<TMP>")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, cmd, "--explain="+mode)
	if res.ExitCode != 0 {
		t.Fatalf("`%s --explain=%s` exit = %d, want 0\nstderr:\n%s", cmd, mode, res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`%s --explain=%s` wrote to stderr:\n%s", cmd, mode, res.Stderr)
	}
	return norm.Apply(res.Stdout)
}

// TestExplainPlanGolden freezes the human and JSON plan output for every
// command×mode combination. fix and lint emit a single plan (their JSON is parsed
// and shape-checked); check emits a fix plan followed by a lint plan, so its JSON
// is two concatenated objects — frozen as a golden but not parsed as one document.
func TestExplainPlanGolden(t *testing.T) {
	for _, cmd := range []string{"check", "fix", "lint"} {
		for _, mode := range []string{"summary", "detailed", "json"} {
			t.Run(cmd+"_"+mode, func(t *testing.T) {
				got := runExplain(t, cmd, mode)
				clitest.AssertGolden(t, cmd+"_explain_"+mode, got)

				if mode != "json" {
					return
				}
				switch cmd {
				case "check":
					// fix-then-lint: two plan objects. Assert both operations appear
					// rather than parsing the (non-single-document) output.
					if !strings.Contains(got, `"operation": "fix"`) || !strings.Contains(got, `"operation": "lint"`) {
						t.Errorf("`check --explain=json` should contain both fix and lint plans:\n%s", got)
					}
				default:
					var plan planShape
					if err := json.Unmarshal([]byte(got), &plan); err != nil {
						t.Fatalf("`%s --explain=json` is not valid JSON: %v\n%s", cmd, err, got)
					}
					if plan.Operation != cmd {
						t.Errorf("operation = %q, want %q", plan.Operation, cmd)
					}
					if plan.RootPath != "<TMP>" {
						t.Errorf("rootPath = %q, want masked <TMP>", plan.RootPath)
					}
					if len(plan.Groups) == 0 {
						t.Fatalf("`%s --explain=json` has no groups:\n%s", cmd, got)
					}
					if plan.Skipped == nil {
						t.Errorf("`%s --explain=json` skipped should be an array, not null", cmd)
					}
					// Every planned task must name a tool, its backing app, and a scope.
					for _, g := range plan.Groups {
						for _, pg := range g.ParallelGroups {
							for _, task := range pg.Tasks {
								if task.ToolName == "" || task.App == "" || task.Scope == "" {
									t.Errorf("incomplete task in plan: %+v", task)
								}
							}
						}
					}
				}
			})
		}
	}
}

// TestExplainToolsSelection proves `--tools` narrows the plan: `lint --tools alpha`
// plans only alpha, dropping beta. The JSON form is parsed to assert exactly one
// tool survives.
func TestExplainToolsSelection(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("tools.config.js", explainToolsConfigJS)
	norm := clitest.NewNormalizer().MaskPath(p.Dir, "<TMP>")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "lint", "--tools", "alpha", "--explain=json")
	if res.ExitCode != 0 {
		t.Fatalf("`lint --tools alpha --explain=json` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	var plan planShape
	if err := json.Unmarshal([]byte(norm.Apply(res.Stdout)), &plan); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, res.Stdout)
	}
	names := map[string]bool{}
	for _, g := range plan.Groups {
		for _, pg := range g.ParallelGroups {
			for _, task := range pg.Tasks {
				names[task.ToolName] = true
			}
		}
	}
	if !names["alpha"] || names["beta"] {
		t.Errorf("`--tools alpha` should plan only alpha, got tools: %v", names)
	}
}

// TestExplainFlagMatrix proves the remaining flags parse and behave offline in
// explain mode: --file-scoped (no staged files → empty set), --fail-on-skip (a
// no-op in explain mode, since nothing runs), a positional file arg, and a
// combination. Each must exit 0; explain output goes to stdout, never stderr.
func TestExplainFlagMatrix(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"file-scoped", []string{"lint", "--file-scoped", "--explain=summary"}},
		{"fail-on-skip", []string{"lint", "--fail-on-skip", "--explain=summary"}},
		{"positional-file", []string{"lint", "README.md", "--explain=summary"}},
		{"combined", []string{"check", "--tools", "alpha", "--file-scoped", "--fail-on-skip", "--explain=detailed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("tools.config.js", explainToolsConfigJS)
			p.WriteFile("README.md", "# test\n")

			args := append([]string{"--no-auto-config", "--config", cfg}, tc.args...)
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, args...)
			if res.ExitCode != 0 {
				t.Fatalf("`%s` exit = %d, want 0\nstderr:\n%s", strings.Join(tc.args, " "), res.ExitCode, res.Stderr)
			}
			if res.Stderr != "" {
				t.Errorf("`%s` wrote to stderr:\n%s", strings.Join(tc.args, " "), res.Stderr)
			}
		})
	}
}

// TestExplainErrorPaths locks the two offline error paths shared by check/fix/lint:
// an unknown --tools value (planning fails naming the missing tool) and a bad
// --explain mode (validated before planning). Both exit non-zero with a clear
// message and no usage block (SilenceUsage).
func TestExplainErrorPaths(t *testing.T) {
	for _, cmd := range []string{"check", "fix", "lint"} {
		t.Run(cmd+"_unknown-tool", func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("tools.config.js", explainToolsConfigJS)

			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, cmd, "--tools", "nonesuch", "--explain=summary")
			if res.ExitCode == 0 {
				t.Fatalf("`%s --tools nonesuch` exit = 0, want non-zero\nstdout:\n%s", cmd, res.Stdout)
			}
			if !strings.Contains(res.Stderr, "tools not found") || !strings.Contains(res.Stderr, "nonesuch") {
				t.Errorf("stderr should name the missing tool:\n%s", res.Stderr)
			}
			if strings.Contains(res.Stderr, "Usage:") {
				t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
			}
		})

		t.Run(cmd+"_bad-explain-mode", func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("tools.config.js", explainToolsConfigJS)

			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, cmd, "--explain=bogus")
			if res.ExitCode == 0 {
				t.Fatalf("`%s --explain=bogus` exit = 0, want non-zero\nstdout:\n%s", cmd, res.Stdout)
			}
			if !strings.Contains(res.Stderr, "invalid --explain value") {
				t.Errorf("stderr should report the invalid explain mode:\n%s", res.Stderr)
			}
			if strings.Contains(res.Stderr, "Usage:") {
				t.Errorf("runtime error must not print usage:\n%s", res.Stderr)
			}
		})
	}
}
