//go:build !windows

package cli_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// A second signal ends the process at once, without waiting for the tools it
// is stopping: here one that ignores SIGTERM, which the first signal's stop
// would otherwise wait out.
func TestExecutionSecondSignal(t *testing.T) {
	e := newExecProject(t, map[string]string{"fixture.marker": ""}, fixtureSpec,
		clitest.ShellTool("stubborn", `trap '' TERM; echo $$ > "$MARKERS/pid"; echo "$0 started" >> "$MARKERS/$0"; sleep 30`,
			clitest.ToolOpSpec{}))
	proc := clitest.Start(t, clitest.RunOptions{Dir: e.p.Dir, CacheDir: e.cache},
		"--no-auto-config", "--config", e.cfg, "lint")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if ran, _ := e.p.Marker("stubborn"); ran {
			break
		}
		if time.Now().After(deadline) {
			_ = proc.Signal(syscall.SIGKILL)
			t.Fatalf("stubborn never started:\n%+v", proc.Wait())
		}
	}
	// The binary dies without stopping the tool, which outlives it.
	t.Cleanup(func() {
		data, err := os.ReadFile(filepath.Join(e.p.Dir, clitest.MarkerDirName, "pid"))
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})
	if err := proc.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := proc.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send the second SIGINT: %v", err)
	}
	sent := time.Now()
	res := proc.Wait()
	if took := time.Since(sent); took > 3*time.Second {
		t.Errorf("the run ended %s after the second signal; it should end at once", took)
	}
	e.wantExit(res, -1)
}
