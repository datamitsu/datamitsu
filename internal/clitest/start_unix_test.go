//go:build !windows

package clitest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fatalTB is the real test's testing.TB with Fatalf recorded instead of
// failing the test, unwinding like *testing.T does.
type fatalTB struct {
	testing.TB

	msg string
}

func (f *fatalTB) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// A test that ends between Start and Wait, or a run that outlives its timeout,
// leaves neither the binary nor the tools it started running: the tools run in
// process groups of their own, so only the binary stopping them cleans them up.
func TestStartCleanupStopsTheTools(t *testing.T) {
	RequireShell(t, "that a started run's tools are stopped when its test ends early")
	tests := []struct {
		name string
		run  func(t *testing.T, args []string, dir, pidFile string)
	}{
		{
			name: "the test ends before Wait",
			run: func(t *testing.T, args []string, dir, pidFile string) {
				t.Helper()
				t.Run("ends before Wait", func(t *testing.T) {
					Start(t, RunOptions{Dir: dir}, args...)
					for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
						if data, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(data), "\n") {
							return
						}
						if time.Now().After(deadline) {
							t.Fatal("the tool never started")
						}
					}
				})
			},
		},
		{
			name: "the run times out",
			run: func(t *testing.T, args []string, dir, _ string) {
				t.Helper()
				tb := &fatalTB{TB: t}
				done := make(chan struct{})
				go func() {
					defer close(done)
					Start(tb, RunOptions{Dir: dir, Timeout: 4 * time.Second}, args...).Wait()
				}()
				<-done
				if !strings.Contains(tb.msg, "timed out") {
					t.Fatalf("Wait() failed with %q, want a timeout", tb.msg)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewProject(t)
			MarkerDir(p)
			p.WriteFile("fixture.marker", "")
			cfg := p.WriteFile("cleanup.config.js", ShellConfig(
				ShellConfigSpec{ProjectTypes: map[string][]string{"fixture": {"fixture.marker"}}},
				ShellTool("sleeper", `echo $$ > "$MARKERS/pid"; sleep 30`, ToolOpSpec{}),
			))
			pidFile := filepath.Join(p.Dir, MarkerDirName, "pid")

			tt.run(t, []string{"--no-auto-config", "--config", cfg, "lint"}, p.Dir, pidFile)

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
		})
	}
}
