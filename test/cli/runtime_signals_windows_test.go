//go:build windows

package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"golang.org/x/sys/windows"
)

func TestRuntimeRunWindowsJobCancellation(t *testing.T) {
	cmdExe, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip("cmd.exe is unavailable")
	}
	p, cfg, _ := runtimeProject(t, cmdExe)
	ready := filepath.Join(p.Dir, "runtime-ready")
	command := fmt.Sprintf("echo ready>\"%s\" & ping -t 127.0.0.1 > nul", ready)
	child := exec.Command(clitest.BuildOnce(t), runtimeArgs(p, cfg, "run", "zed", "--", "/c", command)...)
	child.Dir, child.Env = p.Dir, clitest.BaseEnv(t.TempDir())
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
		}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case waitErr := <-done:
			t.Fatalf("runtime exited before starting its child: %v\nstdout:\n%s\nstderr:\n%s", waitErr, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime child did not become ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(child.Process.Pid)); err != nil {
		t.Fatalf("send console interrupt to runtime process group: %v", err)
	}
	select {
	case waitErr := <-done:
		if code := clitest.ExitCodeOf(waitErr); code != 130 {
			t.Fatalf("runtime exit = %d, want forced cancellation 130 (err %v)\nstdout:\n%s\nstderr:\n%s", code, waitErr, stdout.String(), stderr.String())
		}
	case <-time.After(20 * time.Second):
		_ = child.Process.Kill()
		t.Fatal("runtime process did not stop after console interrupt")
	}
}
