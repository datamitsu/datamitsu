//go:build !windows

package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// A stopped tool leaves nothing behind: a process it started that ignores
// SIGTERM is killed with the rest of its group once the grace has passed.
func TestStoppedToolLeavesNoDescendant(t *testing.T) {
	grace := stopGrace
	stopGrace = 200 * time.Millisecond
	t.Cleanup(func() { stopGrace = grace })

	root := t.TempDir()
	pidFile := filepath.Join(root, "pid")
	script := `(trap '' TERM; exec sleep 30 </dev/null >/dev/null 2>&1) & echo $! > ` + pidFile + `; wait`
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{"alpha": shellApp(script)}}
	task := lintTask(t, "alpha", config.ToolScopePerProject, root)
	e := NewExecutor(root, false, true, appManager, nil)

	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan ExecutionResult, 1)
	go func() { done <- e.executeTask(ctx, task) }()

	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0; time.Sleep(10 * time.Millisecond) {
		if data, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(data), "\n") {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if time.Now().After(deadline) {
			cancel(errors.New("test gave up"))
			<-done
			t.Fatal("the tool never started its child")
		}
	}
	cancel(errors.New("interrupted"))
	if result := <-done; !result.IsCancelled() {
		t.Errorf("result = %+v, want a cancelled task", result)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ended(pid) {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("the tool's child (pid %d) outlived the stopped run", pid)
}

// ended reports whether pid is gone or a zombie: a killed orphan stays one
// until the host's init reaps it, which a container's may never do.
func ended(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The command name before the state may itself hold ") ".
	i := strings.LastIndex(string(stat), ") ")
	return i >= 0 && strings.HasPrefix(string(stat)[i+2:], "Z")
}
