//go:build linux

package tooling

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A group whose processes have all exited is not alive, even while they are
// zombies nobody has reaped yet: kill(-pgid, 0) still answers for them.
func TestGroupAlive(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "exec sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	if !groupAlive(pgid) {
		t.Error("groupAlive() = false for a running group")
	}

	_ = cmd.Process.Kill()
	for deadline := time.Now().Add(5 * time.Second); stillRunning(pgid); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = cmd.Wait()
			t.Fatal("the killed process never exited")
		}
	}
	if syscall.Kill(-pgid, 0) != nil {
		t.Error("kill(-pgid, 0) fails for a group of zombies; the case is not exercised")
	}
	if groupAlive(pgid) {
		t.Error("groupAlive() = true for a group of zombies")
	}

	_ = cmd.Wait()
	if groupAlive(pgid) {
		t.Error("groupAlive() = true for a reaped group")
	}
}
