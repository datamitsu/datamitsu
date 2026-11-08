//go:build windows

package cmd

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRuntimeAssignmentFailureTerminatesUnassignedChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	job, err := createRuntimeJob()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^$")
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: runtimeCreateSuspended}
	var process windows.Handle
	handedOff := false
	defer func() {
		if handedOff {
			return
		}
		if child.Process != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
		if process != 0 {
			_ = windows.CloseHandle(process)
		}
		_ = windows.CloseHandle(job)
	}()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	process, err = windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA|windows.PROCESS_SUSPEND_RESUME|windows.SYNCHRONIZE, false, uint32(child.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.AssignProcessToJobObject(0, process); err == nil {
		t.Fatal("invalid job handle unexpectedly accepted the suspended child")
	}

	done := make(chan struct{})
	handedOff = true
	go func() {
		terminateRuntimeChild(process, job, child)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = child.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("startup cleanup did not finish after watchdog termination")
		}
		t.Fatal("startup cleanup waited for an unassigned suspended child instead of terminating it")
	}
	if child.ProcessState == nil || !child.ProcessState.Exited() || child.ProcessState.ExitCode() != 1 {
		t.Fatalf("unassigned child was not terminated and reaped: %v", child.ProcessState)
	}
}
