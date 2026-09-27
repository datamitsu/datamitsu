//go:build linux || darwin || freebsd

package tooling

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A stop counts when it went out while the process still ran, whatever the
// process then exits with. One that reaches a process after it exited, before
// it is reaped, is delivered all the same and leaves the exit status its own.
func TestTrackStop(t *testing.T) {
	tests := []struct {
		name        string
		script      string
		exitFirst   bool
		wantStopped bool
		wantExit    int
	}{
		{name: "exited before the stop", script: "exit 7", exitFirst: true, wantStopped: false, wantExit: 7},
		{name: "dies to the stop", script: `touch "$0"; sleep 30`, wantStopped: true, wantExit: -1},
		{name: "handles the stop and exits 1", script: `trap 'exit 1' TERM; touch "$0"; while :; do sleep 0.05; done`, wantStopped: true, wantExit: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", tt.script, ready)
			setupProcessGroupCleanup(cmd)
			stop := trackStop(cmd)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(5 * time.Second); !reached(cmd, ready, tt.exitFirst); time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal("the process never reached the state the case needs")
				}
			}
			if err := cmd.Cancel(); err != nil {
				t.Fatalf("Cancel() = %v, want the stop delivered", err)
			}
			err := cmd.Wait()
			if got := stop.stopped.Load(); got != tt.wantStopped {
				t.Errorf("stopped = %v, want %v", got, tt.wantStopped)
			}
			if stop.requested.Load() == 0 {
				t.Error("the stop was not recorded as sent")
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tt.wantExit {
				t.Errorf("Wait() = %v, want exit code %d", err, tt.wantExit)
			}
			if stillRunning(cmd.Process.Pid) {
				t.Error("stillRunning() = true for a reaped process")
			}
		})
	}
}

// reached reports whether the process has exited (exitFirst) or is running
// with its signal handling in place.
func reached(cmd *exec.Cmd, ready string, exitFirst bool) bool {
	if exitFirst {
		return !stillRunning(cmd.Process.Pid)
	}
	_, err := os.Stat(ready)
	return err == nil
}
