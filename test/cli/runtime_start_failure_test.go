package cli_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestRuntimeRunReportsInvalidExecutableWithoutShellFallback(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "invalid-runtime"+exeSuffix())
	if err := os.WriteFile(executable, []byte("\x00invalid executable format\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	project, configPath, _ := runtimeProject(t, executable)
	result := clitest.Run(t, clitest.RunOptions{Dir: project.Dir, Timeout: 10 * time.Second}, runtimeArgs(project, configPath, "run", "zed", "--", "unused")...)
	if result.ExitCode != 1 {
		t.Fatalf("invalid runtime exit = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if result.Stdout != "" || result.Stderr == "" {
		t.Fatalf("start failure omitted its diagnostic or wrote child output: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}
