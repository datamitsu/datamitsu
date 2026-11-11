//go:build !windows

package launch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDispatchInheritsCallerEnvironmentWithoutOverrides(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	result := filepath.Join(t.TempDir(), "result with spaces")
	launcher, err := Ensure("/bin/sh", []string{"-c", "printf 'inherited' > \"$RESULT\""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, handled := DispatchWith([]string{launcher}, []string{"RESULT=" + result}, launcher, func(executable string, args, environ []string) int {
		command := exec.CommandContext(ctx, executable, args[1:]...)
		command.Env = environ
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("inherited environment command failed: %v, %s", err, output)
			return 1
		}
		return 0
	})
	if !handled || code != 0 {
		t.Fatalf("launcher did not execute its recorded command: handled=%v exit=%d", handled, code)
	}
	data, err := os.ReadFile(result)
	if err != nil || string(data) != "inherited" {
		t.Fatalf("caller environment was not honored: %q, %v", data, err)
	}
}
