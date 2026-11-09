package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestRuntimeHumanInventoryExplainsDirectRunCapability(t *testing.T) {
	project, configPath, _ := runtimeProject(t, runtimeProbe(t))
	result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, CacheDir: t.TempDir()}, runtimeArgs(project, configPath, "list")...)
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("human inventory failed: %+v", result)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	columns := make(map[string]int)
	for index, name := range strings.Fields(lines[0]) {
		columns[name] = index
	}
	for _, required := range []string{"NAME", "STATUS", "RUNNABLE", "REASON"} {
		if _, ok := columns[required]; !ok {
			t.Fatalf("inventory hides %s: %s", required, result.Stdout)
		}
	}
	rows := make(map[string][]string)
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) <= columns["RUNNABLE"] {
			t.Fatalf("inventory row omits capability: %q", line)
		}
		rows[fields[columns["NAME"]]] = fields
	}
	for _, name := range []string{"alpha", "zed"} {
		row, ok := rows[name]
		if !ok || row[columns["STATUS"]] != "installed" || row[columns["RUNNABLE"]] != "yes" {
			t.Fatalf("installed interpreter %s is not visibly runnable: %s", name, result.Stdout)
		}
	}
	pnpm, ok := rows["pnpm"]
	if !ok || pnpm[columns["STATUS"]] != "missing" || pnpm[columns["RUNNABLE"]] != "no" {
		t.Fatalf("pnpm capability and availability are not distinguished: %s", result.Stdout)
	}
	if len(pnpm) <= columns["REASON"] {
		t.Fatalf("pnpm has no visible explanation: %s", result.Stdout)
	}
	reason := strings.Join(pnpm[columns["REASON"]:], " ")
	if !strings.Contains(reason, "package manager") || !strings.Contains(reason, "datamitsu exec") || !strings.Contains(reason, "missing-pnpm-command") {
		t.Fatalf("pnpm must explain its restriction, alternative and missing executable: %q", reason)
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
