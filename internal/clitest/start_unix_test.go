//go:build !windows

package clitest

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A test that ends between Start and Wait leaves neither the binary nor the
// tools it started running: the tools run in process groups of their own, so
// only the binary stopping them cleans them up.
func TestStartCleanupStopsTheTools(t *testing.T) {
	RequireShell(t, "that a started run's tools are stopped when its test ends early")
	p := NewProject(t)
	MarkerDir(p)
	p.WriteFile("fixture.marker", "")
	cfg := p.WriteFile("cleanup.config.js", ShellConfig(
		ShellConfigSpec{ProjectTypes: map[string][]string{"fixture": {"fixture.marker"}}},
		ShellTool("sleeper", `echo $$ > "$MARKERS/pid"; sleep 30`, ToolOpSpec{}),
	))
	pidFile := filepath.Join(p.Dir, MarkerDirName, "pid")

	t.Run("ends before Wait", func(t *testing.T) {
		Start(t, RunOptions{Dir: p.Dir}, "--no-auto-config", "--config", cfg, "lint")
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if data, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(data), "\n") {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the tool never started")
			}
		}
	})

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read the tool's pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse the tool's pid %q: %v", data, err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	t.Errorf("the tool (pid %d) is still running after its test ended", pid)
}
