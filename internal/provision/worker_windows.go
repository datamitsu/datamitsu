//go:build windows

package provision

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	workerCreateSuspended                    = 0x00000004
	workerCreateNewProcessGroup              = 0x00000200
	workerCreateUnicodeEnvironment           = 0x00000400
	workerExtendedStartupInfoPresent         = 0x00080000
	workerHandleListAttribute        uintptr = 0x00020002
)

// Worker keeps provisioning alive until its handlers have stopped every installer.
type Worker struct {
	Cmd      *exec.Cmd
	Client   *Client
	liveness *os.File
	cancel   *os.File
	done     chan struct{}
	waitErr  error
}

// StartWorker isolates private handles without tying worker lifetime to the parent's handles.
func StartWorker(ctx context.Context) (_ *Worker, err error) {
	var files []*os.File
	defer func() {
		if err != nil {
			closeWorkerFiles(files...)
		}
	}()
	pipe := func(label string) (*os.File, *os.File, error) {
		r, w, pipeErr := os.Pipe()
		if pipeErr != nil {
			return nil, nil, fmt.Errorf("create provision %s pipe: %w", label, pipeErr)
		}
		files = append(files, r, w)
		return r, w, nil
	}
	requestRead, requestWrite, err := pipe("request")
	if err != nil {
		return nil, err
	}
	responseRead, responseWrite, err := pipe("response")
	if err != nil {
		return nil, err
	}
	liveRead, liveWrite, err := pipe("liveness")
	if err != nil {
		return nil, err
	}
	cancelRead, cancelWrite, err := pipe("cancellation")
	if err != nil {
		return nil, err
	}
	handshakeRead, handshakeWrite, err := pipe("handshake")
	if err != nil {
		return nil, err
	}
	childHandles, err := duplicateInheritedHandles([]*os.File{requestRead, responseWrite, liveRead, cancelRead, handshakeRead})
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, handle := range childHandles {
			_ = windows.CloseHandle(handle)
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate provision worker executable: %w", err)
	}
	argv := []string{"--internal-provision-worker"}
	for _, handle := range childHandles {
		argv = append(argv, strconv.FormatUint(uint64(handle), 10))
	}
	// #nosec G204 -- Re-execute this binary with private pipe handles, never a configured command or shell.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), executable, argv...)
	cmd.Cancel = nil
	process, thread, err := startSuspended(cmd, childHandles)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = windows.CloseHandle(thread)
		if err != nil {
			_ = windows.TerminateProcess(process, 1)
			_, _ = windows.WaitForSingleObject(process, windows.INFINITE)
			_ = cmd.Wait()
			_ = windows.CloseHandle(process)
		}
	}()
	closeWorkerFiles(requestRead, responseWrite, liveRead, cancelRead, handshakeRead)
	if _, err = windows.ResumeThread(thread); err != nil {
		return nil, fmt.Errorf("resume provision worker: %w", err)
	}
	var nonce [startupNonceSize]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("create provision startup nonce: %w", err)
	}
	if _, err = handshakeWrite.Write(nonce[:]); err != nil {
		return nil, fmt.Errorf("send provision startup handshake: %w", err)
	}
	if err = handshakeWrite.Close(); err != nil {
		return nil, fmt.Errorf("close provision startup handshake: %w", err)
	}
	client := NewClient(&duplex{r: responseRead, w: requestWrite}, nil)
	worker := &Worker{Cmd: cmd, Client: client, liveness: liveWrite, cancel: cancelWrite, done: make(chan struct{})}
	go func() {
		worker.waitErr = cmd.Wait()
		_ = windows.CloseHandle(process)
		close(worker.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = worker.Cancel()
			_ = worker.liveness.Close()
		case <-worker.done:
		}
	}()
	return worker, nil
}

// OpenWorkerPipes wraps the five private handles passed to the worker and clears inheritance.
func OpenWorkerPipes(args []string) (rpc io.ReadWriteCloser, liveness io.ReadCloser, cancel io.ReadCloser, handshake io.Reader, err error) {
	if len(args) != 5 {
		return nil, nil, nil, nil, errors.New("provision worker requires five inherited handles")
	}
	handles := make([]*os.File, 5)
	for i := range handles {
		value, parseErr := strconv.ParseUint(args[i], 10, strconv.IntSize)
		if parseErr != nil || value == 0 {
			closeWorkerFiles(handles...)
			return nil, nil, nil, nil, fmt.Errorf("invalid provision worker handle %q", args[i])
		}
		handles[i] = os.NewFile(uintptr(value), "provision-worker")
		if handles[i] == nil {
			closeWorkerFiles(handles...)
			return nil, nil, nil, nil, errors.New("invalid inherited provision worker handle")
		}
		if err := windows.SetHandleInformation(windows.Handle(value), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			closeWorkerFiles(handles...)
			return nil, nil, nil, nil, fmt.Errorf("secure inherited provision handle: %w", err)
		}
	}
	return &duplex{r: handles[0], w: handles[1]}, handles[2], handles[3], handles[4], nil
}

func duplicateInheritedHandles(files []*os.File) ([]windows.Handle, error) {
	current, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	handles := make([]windows.Handle, len(files))
	for i, file := range files {
		if err := windows.DuplicateHandle(current, windows.Handle(file.Fd()), current, &handles[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			for _, handle := range handles[:i] {
				_ = windows.CloseHandle(handle)
			}
			return nil, err
		}
	}
	return handles, nil
}

func startSuspended(cmd *exec.Cmd, handles []windows.Handle) (windows.Handle, windows.Handle, error) {
	exe, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return 0, 0, err
	}
	line, err := windows.UTF16FromString(joinWindowsArgs(append([]string{cmd.Path}, cmd.Args[1:]...)))
	if err != nil {
		return 0, 0, err
	}
	var cwd *uint16
	if cmd.Dir != "" {
		cwd, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return 0, 0, err
		}
	}
	attributeList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, 0, err
	}
	defer attributeList.Delete()
	if err := attributeList.Update(workerHandleListAttribute, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return 0, 0, err
	}
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))}, ProcThreadAttributeList: attributeList.List()}
	var pi windows.ProcessInformation
	flags := uint32(workerCreateSuspended | workerCreateNewProcessGroup | workerCreateUnicodeEnvironment | workerExtendedStartupInfoPresent)
	if err := windows.CreateProcess(exe, &line[0], nil, nil, true, flags, nil, cwd, &si.StartupInfo, &pi); err != nil {
		return 0, 0, err
	}
	process, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
		return 0, 0, fmt.Errorf("open provision worker process: %w", err)
	}
	cmd.Process = process
	return pi.Process, pi.Thread, nil
}

func joinWindowsArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windows.EscapeArg(arg)
	}
	return strings.Join(quoted, " ")
}

// Cancel requests cooperative shutdown without terminating the store-owning worker.
func (w *Worker) Cancel() error {
	if w == nil || w.cancel == nil {
		return nil
	}
	_, err := w.cancel.Write([]byte{1})
	if err != nil {
		return fmt.Errorf("cancel provision worker: %w", err)
	}
	return nil
}

// Close waits for operation cleanup and preserves the worker's exit status.
func (w *Worker) Close() error {
	if w == nil {
		return nil
	}
	_ = w.liveness.Close()
	_ = w.Client.Close()
	// A second signal must still reach the worker while installer cleanup is pending.
	<-w.done
	_ = w.cancel.Close()
	if w.waitErr != nil {
		return fmt.Errorf("wait for provision worker: %w", w.waitErr)
	}
	return nil
}

type duplex struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (d *duplex) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err != nil {
		return n, fmt.Errorf("read provision pipe: %w", err)
	}
	return n, nil
}

func (d *duplex) Write(p []byte) (int, error) {
	n, err := d.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("write provision pipe: %w", err)
	}
	return n, nil
}

func (d *duplex) Close() error {
	if err := errors.Join(d.r.Close(), d.w.Close()); err != nil {
		return fmt.Errorf("close provision pipes: %w", err)
	}
	return nil
}

func closeWorkerFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
