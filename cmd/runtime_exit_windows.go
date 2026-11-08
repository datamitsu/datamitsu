//go:build windows

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const runtimeCreateSuspended = 0x00000004

func executeRuntimeRequest(request runtimeExecRequest) {
	// The executable is the resolved configured interpreter; arguments remain
	// discrete argv elements and are never passed through a command shell.
	child := exec.Command(request.Executable, request.Args...) //nolint:gosec // G204: direct execution of the resolved runtime executable, with no shell expansion
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = os.Environ()
	for key, value := range request.Env {
		child.Env = replaceEnv(child.Env, key, value)
	}
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: runtimeCreateSuspended}

	job, err := createRuntimeJob()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error: start runtime:", err)
		os.Exit(1)
	}
	if err := child.Start(); err != nil {
		_ = windows.CloseHandle(job)
		_, _ = fmt.Fprintln(os.Stderr, "error: start runtime:", err)
		os.Exit(1)
	}
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA|windows.PROCESS_SUSPEND_RESUME|windows.SYNCHRONIZE, false, uint32(child.Process.Pid))
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		_ = windows.CloseHandle(job)
		_, _ = fmt.Fprintln(os.Stderr, "error: start runtime:", err)
		os.Exit(1)
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		terminateRuntimeChild(process, job, child)
		_, _ = fmt.Fprintln(os.Stderr, "error: start runtime:", err)
		os.Exit(1)
	}
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(process))
	if status != 0 {
		terminateRuntimeChild(process, job, child)
		_, _ = fmt.Fprintf(os.Stderr, "error: start runtime: NtResumeProcess: NTSTATUS 0x%x\n", status)
		os.Exit(1)
	}
	_ = windows.CloseHandle(process)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	var waitErr error
	forced := false
	select {
	case waitErr = <-done:
	case <-interrupt:
		forced = true
		_ = windows.TerminateJobObject(job, 130)
		waitErr = <-done
	}
	_ = windows.CloseHandle(job)
	if forced {
		os.Exit(130)
	}
	if waitErr != nil {
		var status *exec.ExitError
		if errors.As(waitErr, &status) {
			os.Exit(status.ExitCode())
		}
		_, _ = fmt.Fprintln(os.Stderr, "error: runtime:", waitErr)
		os.Exit(1)
	}
	os.Exit(0)
}

func createRuntimeJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func terminateRuntimeChild(process, job windows.Handle, child *exec.Cmd) {
	_ = windows.TerminateJobObject(job, 1)
	// Assignment may have failed, leaving the suspended child outside the job.
	_ = windows.TerminateProcess(process, 1)
	_, _ = windows.WaitForSingleObject(process, windows.INFINITE)
	_ = windows.CloseHandle(process)
	_ = windows.CloseHandle(job)
	_ = child.Wait()
}

func replaceEnv(values []string, key, value string) []string {
	prefix := key + "="
	for i, current := range values {
		if len(current) >= len(prefix) && current[:len(prefix)] == prefix {
			values[i] = prefix + value
			return values
		}
	}
	return append(values, prefix+value)
}
