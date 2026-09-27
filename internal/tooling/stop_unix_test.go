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
// it is reaped, is delivered all the same and leaves the exit status its own,
// a success included.
func TestTrackStop(t *testing.T) {
	tests := []struct {
		name        string
		script      string
		exitFirst   bool
		wantStopped bool
		wantExit    int
	}{
		{name: "exited 0 before the stop", script: "exit 0", exitFirst: true, wantStopped: false, wantExit: 0},
		{name: "exited 7 before the stop", script: "exit 7", exitFirst: true, wantStopped: false, wantExit: 7},
		{name: "dies to the stop", script: `touch "$0"; sleep 30`, wantStopped: true, wantExit: -1},
		{name: "handles the stop and exits 1", script: `trap 'exit 1' TERM; touch "$0"; while :; do sleep 0.05; done`, wantStopped: true, wantExit: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", tt.script, ready)
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
			// The stop has to go out before Wait reaps the process, as it does
			// when a run is cancelled while the process runs or while its
			// exit is not yet collected.
			cancel()
			for deadline := time.Now().Add(5 * time.Second); stop.requested.Load() == 0; time.Sleep(time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the cancellation never sent the stop")
				}
			}
			err := cmd.Wait()
			if got := stop.stopped.Load(); got != tt.wantStopped {
				t.Errorf("stopped = %v, want %v", got, tt.wantStopped)
			}
			exit := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("Wait() = %v, want the process's own exit", err)
				}
				exit = exitErr.ExitCode()
			}
			if exit != tt.wantExit {
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
