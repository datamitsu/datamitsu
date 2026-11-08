//go:build !windows

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
	"syscall"
)

// Worker owns the private worker process and its communication pipes.
type Worker struct {
	Cmd      *exec.Cmd
	Client   *Client
	liveness *os.File
	cancel   io.WriteCloser
	done     chan struct{}
	waitErr  error
}

// StartWorker launches the current executable with dedicated RPC, liveness, cancel, and handshake pipes.
func StartWorker(ctx context.Context) (*Worker, error) {
	rpcChild, rpcParent, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create provision RPC pipe: %w", err)
	}
	rpcRead, rpcWrite, err := os.Pipe()
	if err != nil {
		closeFiles([]*os.File{rpcChild, rpcParent})
		return nil, fmt.Errorf("create provision response pipe: %w", err)
	}
	liveRead, liveWrite, err := os.Pipe()
	if err != nil {
		closeFiles([]*os.File{rpcChild, rpcParent, rpcRead, rpcWrite})
		return nil, fmt.Errorf("create provision liveness pipe: %w", err)
	}
	cancelRead, cancelWrite, err := os.Pipe()
	if err != nil {
		closeFiles([]*os.File{rpcChild, rpcParent, rpcRead, rpcWrite, liveRead, liveWrite})
		return nil, fmt.Errorf("create provision cancellation pipe: %w", err)
	}
	handshakeRead, handshakeWrite, err := os.Pipe()
	if err != nil {
		closeFiles([]*os.File{rpcChild, rpcParent, rpcRead, rpcWrite, liveRead, liveWrite, cancelRead, cancelWrite})
		return nil, fmt.Errorf("create provision handshake pipe: %w", err)
	}
	var nonce [startupNonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		closeFiles([]*os.File{handshakeRead, handshakeWrite})
		closeFiles([]*os.File{rpcChild, rpcParent, rpcRead, rpcWrite, liveRead, liveWrite, cancelRead, cancelWrite})
		return nil, fmt.Errorf("create provision startup nonce: %w", err)
	}
	workerArgs := []string{"--internal-provision-worker", "3", "4", "5", "6", "7"}
	executable, err := os.Executable()
	if err != nil {
		closeFiles([]*os.File{rpcChild, rpcParent, rpcRead, rpcWrite, liveRead, liveWrite, cancelRead, cancelWrite, handshakeRead, handshakeWrite})
		return nil, fmt.Errorf("locate provision worker executable: %w", err)
	}
	// Cancellation travels over private pipes so installers finish cleanup before the worker exits.
	// #nosec G204 -- The OS supplies this binary's absolute path; only private pipe descriptors are passed.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), executable, workerArgs...)
	cmd.Cancel = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{rpcChild, rpcWrite, liveRead, cancelRead, handshakeRead}
	if err := cmd.Start(); err != nil {
		closeFiles([]*os.File{rpcChild, rpcParent, rpcRead, rpcWrite, liveRead, liveWrite, cancelRead, cancelWrite, handshakeRead, handshakeWrite})
		return nil, fmt.Errorf("start provision worker: %w", err)
	}
	closeFiles([]*os.File{rpcChild, rpcWrite, liveRead, cancelRead, handshakeRead})
	if _, err := handshakeWrite.Write(nonce[:]); err != nil {
		_ = handshakeWrite.Close()
		killWorkerGroup(cmd)
		_ = cmd.Wait()
		closeFiles([]*os.File{rpcRead, rpcParent, liveWrite, cancelWrite})
		return nil, fmt.Errorf("send provision startup handshake: %w", err)
	}
	if err := handshakeWrite.Close(); err != nil {
		killWorkerGroup(cmd)
		_ = cmd.Wait()
		closeFiles([]*os.File{rpcRead, rpcParent, liveWrite, cancelWrite})
		return nil, fmt.Errorf("close provision startup handshake: %w", err)
	}
	client := NewClient(&duplex{r: rpcRead, w: rpcParent}, nil)
	worker := &Worker{Cmd: cmd, Client: client, liveness: liveWrite, cancel: cancelWrite, done: make(chan struct{})}
	go func() {
		worker.waitErr = cmd.Wait()
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

// OpenWorkerPipes acquires the inherited descriptor numbers passed to the worker entrypoint.
func OpenWorkerPipes(args []string) (rpc io.ReadWriteCloser, liveness io.ReadCloser, cancel io.ReadCloser, handshake io.Reader, err error) {
	if len(args) != 5 {
		return nil, nil, nil, nil, errors.New("provision worker requires five inherited descriptors")
	}
	files := make([]*os.File, 5)
	for i, arg := range args[:5] {
		fd, parseErr := strconv.Atoi(arg)
		if parseErr != nil || fd < 3 {
			closeFiles(files[:i])
			return nil, nil, nil, nil, errors.New("invalid provision worker descriptor")
		}
		files[i] = os.NewFile(uintptr(fd), "provision-worker-pipe")
		if files[i] == nil {
			closeFiles(files[:i])
			return nil, nil, nil, nil, errors.New("invalid provision worker descriptor")
		}
		if _, statErr := files[i].Stat(); statErr != nil {
			closeFiles(files[:i+1])
			return nil, nil, nil, nil, fmt.Errorf("validate provision worker descriptor %d: %w", i, statErr)
		}
	}
	for _, file := range files {
		syscall.CloseOnExec(int(file.Fd()))
	}
	return &duplex{r: files[0], w: files[1]}, files[2], files[3], files[4], nil
}

// Cancel asks the worker to stop active provisioning operations.
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

// Close closes worker communication and waits for the worker process to exit.
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

func killWorkerGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
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

func closeFiles(files []*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
