//go:build windows

package installprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type process struct {
	job    windows.Handle
	handle windows.Handle
}

type jobAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func start(cmd *exec.Cmd) (*process, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create installer job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("configure installer job: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("start installer process: %w", err)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA|windows.PROCESS_SUSPEND_RESUME|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("open installer process: %w", err)
	}
	p := &process{job: job, handle: handle}
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		_ = windows.TerminateProcess(handle, 1)
		_, _ = windows.WaitForSingleObject(handle, windows.INFINITE)
		_ = windows.CloseHandle(handle)
		_ = windows.CloseHandle(job)
		_ = cmd.Wait()
		return nil, fmt.Errorf("assign installer process to job: %w", err)
	}
	ntdll := windows.NewLazySystemDLL("ntdll.dll")
	status, _, _ := ntdll.NewProc("NtResumeProcess").Call(uintptr(handle))
	if status != 0 {
		_ = windows.TerminateJobObject(job, 1)
		_, _ = windows.WaitForSingleObject(handle, windows.INFINITE)
		_ = windows.CloseHandle(handle)
		_ = windows.CloseHandle(job)
		_ = cmd.Wait()
		return nil, fmt.Errorf("resume installer process: NTSTATUS 0x%x", status)
	}
	return p, nil
}

func finish(ctx context.Context, cmd *exec.Cmd, p *process) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var runErr, cleanupErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		cleanupErr = stopJob(p.job)
		runErr = <-done
	}
	if cleanupErr == nil {
		cleanupErr = stopJob(p.job)
	}
	if cleanupErr != nil && runErr == nil {
		runErr = cleanupErr
	}
	_ = windows.CloseHandle(p.handle)
	_ = windows.CloseHandle(p.job)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(ctxErr, runErr)
	}
	return runErr
}

func stopJob(job windows.Handle) error {
	var result error
	for {
		info := jobAccountingInformation{}
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		if err == nil && info.ActiveProcesses == 0 {
			return result
		}
		if err != nil && result == nil {
			result = fmt.Errorf("query installer job: %w", err)
		}
		if err := windows.TerminateJobObject(job, 1); err != nil && result == nil {
			result = fmt.Errorf("terminate installer job: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
