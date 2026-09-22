package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestInspectHelp(t *testing.T) {
	result := clitest.Run(t, clitest.RunOptions{}, "inspect", "--help")
	if result.ExitCode != 0 {
		t.Fatal(result.Stderr)
	}
	clitest.AssertGolden(t, "inspect_help", clitest.NewNormalizer().Apply(result.Stdout))
}

func TestInspectExport(t *testing.T) {
	project := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(project)
	args := []string{"--no-auto-config", "--config", cfg, "inspect", "--output", "-"}
	result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir}, args...)
	if result.ExitCode != 0 {
		t.Fatal(result.Stderr)
	}
	if !strings.HasPrefix(result.Stdout, "<!doctype html>") || !strings.Contains(result.Stdout, `"apps":[]`) || strings.Contains(result.Stdout, "__DATAMITSU_INSPECTOR_MANIFEST__") {
		t.Fatal("stdout is not a populated standalone inspector")
	}
	target := filepath.Join(project.Dir, "atlas.html")
	args[len(args)-1] = target
	written := clitest.Run(t, clitest.RunOptions{Dir: project.Dir}, args...)
	if written.ExitCode != 0 || written.Stdout != "" {
		t.Fatalf("export: %d %s %s", written.ExitCode, written.Stdout, written.Stderr)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != result.Stdout {
		t.Fatal("file differs from stdout artifact")
	}
	args[len(args)-1] = filepath.Join(project.Dir, "missing", "atlas.html")
	failed := clitest.Run(t, clitest.RunOptions{Dir: project.Dir}, args...)
	if failed.ExitCode == 0 {
		t.Fatal("write failure must fail the command")
	}
}

func TestInspectInvalidFlags(t *testing.T) {
	for _, args := range [][]string{{"inspect", "--port", "-1"}, {"inspect", "--port", "65536"}, {"inspect", "--output", ""}, {"inspect", "--output", "-", "--port", "0"}, {"inspect", "unexpected"}} {
		result := clitest.Run(t, clitest.RunOptions{}, args...)
		if result.ExitCode == 0 {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
}
