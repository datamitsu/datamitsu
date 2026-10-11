//go:build windows

package prepare

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// resumeProcess resumes a process created suspended. NtResumeProcess wakes
// every thread of the process, which is exactly the contract the
// suspend-assign-resume sequence needs: exec.Cmd does not expose the primary
// thread handle, and resuming the process as a whole is the supported way to
// release it.
func resumeProcess(handle windows.Handle) error {
	ntdll, err := windows.LoadDLL("ntdll.dll")
	if err != nil {
		return err
	}
	defer ntdll.Release()
	proc, err := ntdll.FindProc("NtResumeProcess")
	if err != nil {
		return err
	}
	r0, _, _ := proc.Call(uintptr(handle))
	if r0 != 0 {
		return syscall.Errno(r0)
	}
	return nil
}
