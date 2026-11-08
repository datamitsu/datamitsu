package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

func TestRuntimeJSONOutputFailureReturnsFailureWithoutChangingTarget(t *testing.T) {
	probe := runtimeProbe(t)
	for _, operation := range []string{"inventory", "execution plan"} {
		t.Run(operation, func(t *testing.T) {
			project, configPath, _ := runtimeProject(t, probe)
			target := filepath.Join(t.TempDir(), "read-only-output")
			if err := os.WriteFile(target, []byte("preserve output target"), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := os.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = output.Close() }()
			args := runtimeArgs(project, configPath, "list", "--json")
			if operation == "execution plan" {
				args = runtimeArgs(project, configPath, "run", "--dry-run", "zed", "--", "unused")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, clitest.BuildOnce(t), args...)
			command.Dir = project.Dir
			command.Env = clitest.BaseEnv(t.TempDir())
			command.Stdout = output
			var diagnostic bytes.Buffer
			command.Stderr = &diagnostic
			var exit *exec.ExitError
			if err := command.Run(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("failed JSON write was not a command failure: error=%v stderr=%q", err, diagnostic.String())
			}
			if diagnostic.Len() == 0 {
				t.Fatal("failed JSON output omitted its diagnostic")
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "preserve output target" {
				t.Fatalf("failed JSON write changed its output target: %q, %v", data, err)
			}
		})
	}
}
