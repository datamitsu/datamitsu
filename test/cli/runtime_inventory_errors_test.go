package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestRuntimeHumanInventoryIncludesMissingAndInstalledRuntimes(t *testing.T) {
	project, configPath, _ := runtimeProject(t, runtimeProbe(t))
	result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir}, runtimeArgs(project, configPath, "list")...)
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("human inventory failed: %+v", result)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	want := [][]string{
		{"NAME", "KIND", "MODE", "VERSION", "STATUS"},
		{"alpha", "node", "system", "22", "installed"},
		{"pnpm", "pnpm", "system", "12", "missing"},
		{"zed", "go", "system", "1.23", "installed"},
	}
	if len(lines) != len(want) {
		t.Fatalf("inventory rows = %q", result.Stdout)
	}
	for index, row := range want {
		if strings.Join(strings.Fields(lines[index]), " ") != strings.Join(row, " ") {
			t.Errorf("inventory row %d = %q, want fields %q", index, lines[index], row)
		}
	}
}

func TestRuntimeCommandsRejectMissingConfiguration(t *testing.T) {
	for _, operation := range []string{"list", "run"} {
		t.Run(operation, func(t *testing.T) {
			project := clitest.NewProject(t)
			configPath := filepath.Join(project.Dir, "missing.config.js")
			args := []string{operation}
			if operation == "run" {
				args = append(args, "zed", "--", "unused")
			}
			result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir}, runtimeArgs(project, configPath, args...)...)
			if result.ExitCode != 1 || result.Stdout != "" || result.Stderr == "" {
				t.Fatalf("missing configuration did not fail before runtime output: %+v", result)
			}
		})
	}
}
