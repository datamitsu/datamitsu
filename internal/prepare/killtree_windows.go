//go:build windows

package prepare

import (
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobMu     sync.Mutex
	jobsByPid = map[int]windows.Handle{}
)

// killTree cancels the step's process tree on Windows: the step joined its
// job object before its primary thread ever ran (setProcessGroup starts it
// suspended, trackProcessTree assigns the job and resumes), so every process
// it created belongs to the job and terminating the job cancels them all —
// downloaders and unzippers do not outlive the timeout.
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	jobMu.Lock()
	job, ok := jobsByPid[pid]
	if ok {
		delete(jobsByPid, pid)
	}
	jobMu.Unlock()
	if ok {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
}

// trackProcessTree assigns a freshly started (still suspended) step process
// to a job object with KILL_ON_JOB_CLOSE and resumes its primary thread. The
// process was created suspended by setProcessGroup, so no descendant can
// exist before the assignment: the job provably covers the whole tree. The
// job handle stays open for the process's lifetime; untrackProcessTree
// closes it on a clean exit, killTree on a timeout.
func trackProcessTree(pid int) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	jobMu.Lock()
	jobsByPid[pid] = job
	jobMu.Unlock()
	if err := resumeProcess(handle); err != nil {
		// The process stays suspended and the caller kills it. Drop the
		// registration through untrackProcessTree, which removes the map
		// entry and closes the handle exactly once: a concurrent killTree
		// may have taken both already (cancellation while resuming), and a
		// bare CloseHandle here could double-close — or close a reused
		// handle.
		untrackProcessTree(pid)
		return fmt.Errorf("resume suspended step process: %w", err)
	}
	return nil
}

// untrackProcessTree drops a job handle after a clean (non-timeout) step exit.
func untrackProcessTree(pid int) {
	jobMu.Lock()
	job, ok := jobsByPid[pid]
	if ok {
		delete(jobsByPid, pid)
	}
	jobMu.Unlock()
	if ok {
		_ = windows.CloseHandle(job)
	}
}

// setProcessGroup is Windows's share of the process-tree contract: the step
// starts suspended (CREATE_SUSPENDED) so trackProcessTree can put it into
// the job object before its first instruction runs — no descendant can
// escape the job by being spawned between Start and assignment.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}
