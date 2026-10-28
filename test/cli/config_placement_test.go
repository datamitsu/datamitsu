package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// noToolsConfigJS is a minimal configuration with no tools: the placement
// check is core behavior and needs no configured tool to run.
const noToolsConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = (config) => ({
  version: "1.0.0",
  tools: {}
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestLintConfigPlacementGolden freezes the core config-placement contract:
// Datamitsu-owned config artifacts below the git root fail `lint` with a
// datamitsu/config-placement finding each, the same names at the root are
// legal, and a clean repository passes silently. The run has no tools and no
// project types, so nothing but the core check can produce output.
func TestLintConfigPlacementGolden(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", noToolsConfigJS)
	p.WriteFile(".datamitsuignore", "*.go: eslint\n")
	p.WriteFile(".datamitsu/keep", "x\n")
	// Violations below the root:
	p.WriteFile("pkg/datamitsu.config.js", "export default {}\n")
	p.WriteFile("sub/.datamitsu/nested", "x\n")
	p.WriteFile("deep/nest/.datamitsuignore", "*.ts: eslint\n")

	norm := clitest.NewNormalizer()
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "lint")
	if res.ExitCode != 1 {
		t.Fatalf("lint exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	out := norm.Apply(res.Stdout)
	for _, want := range []string{"pkg/datamitsu.config.js", "sub/.datamitsu", "deep/nest/.datamitsuignore"} {
		if !strings.Contains(out, want) {
			t.Errorf("lint output lacks %s:\n%s", want, out)
		}
	}
	if strings.Count(out, "config-placement") != 3 {
		t.Errorf("lint output should carry three config-placement findings:\n%s", out)
	}
	clitest.AssertGolden(t, "lint_config_placement", out)
}

// The same repository without violations passes with no datamitsu surface.
func TestLintConfigPlacementCleanGolden(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("datamitsu.config.js", noToolsConfigJS)
	p.WriteFile(".datamitsuignore", "*.go: eslint\n")
	p.WriteFile(".datamitsu/keep", "x\n")
	p.WriteFile("main.go", "package main\n")

	norm := clitest.NewNormalizer()
	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir}, "lint")
	if res.ExitCode != 0 {
		t.Fatalf("lint exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	out := norm.Apply(res.Stdout)
	if strings.Contains(out, "config-placement") || strings.Contains(out, "datamitsu ✗") {
		t.Errorf("a clean repository must not surface the placement check:\n%s", out)
	}
	clitest.AssertGolden(t, "lint_config_placement_clean", out)
}
