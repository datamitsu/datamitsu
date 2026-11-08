//go:build !windows

package provision

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const workerTestHelperEnv = "PROVISION_TEST_MODE"

func TestWorkerSubprocess(t *testing.T) {
	mode := os.Getenv(workerTestHelperEnv)
	if mode != "" {
		workerTestHelper(t, mode)
		return
	}

	t.Run("liveness EOF", func(t *testing.T) {
		worker := startTestWorker(t, "eof")
		if err := worker.liveness.Close(); err != nil {
			t.Fatalf("close liveness: %v", err)
		}
		waitWorkerExit(t, worker)
		if err := worker.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		worker := startTestWorkerContext(ctx, t, "cancel")
		cancel()
		waitWorkerExit(t, worker)
		if err := worker.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	t.Run("exit status survives completion observers", func(t *testing.T) {
		worker := startTestWorker(t, "exit17")
		waitWorkerExit(t, worker)
		if err := worker.Close(); err == nil || worker.Cmd.ProcessState.ExitCode() != 17 {
			t.Fatalf("worker failure lost after completion: error=%v state=%v", err, worker.Cmd.ProcessState)
		}
	})
}

func startTestWorker(t *testing.T, mode string) *Worker {
	t.Helper()
	return startTestWorkerContext(context.Background(), t, mode)
}

func startTestWorkerContext(ctx context.Context, t *testing.T, mode string) *Worker {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerSubprocess$")
	cmd.Env = append(os.Environ(), workerTestHelperEnv+"="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	rpcChildRead, rpcParentWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rpcParentRead, rpcChildWrite, err := os.Pipe()
	if err != nil {
		closeTestFiles(rpcParentRead, rpcParentWrite)
		t.Fatal(err)
	}
	liveRead, liveWrite, err := os.Pipe()
	if err != nil {
		closeTestFiles(rpcParentRead, rpcParentWrite, rpcChildRead, rpcChildWrite)
		t.Fatal(err)
	}
	cancelRead, cancelWrite, err := os.Pipe()
	if err != nil {
		closeTestFiles(rpcParentRead, rpcParentWrite, rpcChildRead, rpcChildWrite, liveRead, liveWrite)
		t.Fatal(err)
	}
	handshakeRead, handshakeWrite, err := os.Pipe()
	if err != nil {
		closeTestFiles(rpcParentRead, rpcParentWrite, rpcChildRead, rpcChildWrite, liveRead, liveWrite, cancelRead, cancelWrite)
		t.Fatal(err)
	}
	cmd.ExtraFiles = []*os.File{rpcChildRead, rpcChildWrite, liveRead, cancelRead, handshakeRead}
	cmd.Env = append(cmd.Env, "PROVISION_TEST_FDS=3,4,5,6,7")
	if err := cmd.Start(); err != nil {
		closeTestFiles(rpcParentRead, rpcParentWrite, rpcChildRead, rpcChildWrite, liveRead, liveWrite, cancelRead, cancelWrite, handshakeRead, handshakeWrite)
		t.Fatal(err)
	}
	closeTestFiles(rpcChildRead, rpcChildWrite, liveRead, cancelRead, handshakeRead)
	if _, err := handshakeWrite.WriteString("test"); err != nil {
		t.Fatal(err)
	}
	if err := handshakeWrite.Close(); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{Cmd: cmd, Client: NewClient(&duplex{r: rpcParentRead, w: rpcParentWrite}, nil), liveness: liveWrite, cancel: cancelWrite, done: make(chan struct{})}
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
	t.Cleanup(func() { _ = worker.Close() })
	return worker
}

func workerTestHelper(t *testing.T, mode string) {
	t.Helper()
	fds := strings.Split(os.Getenv("PROVISION_TEST_FDS"), ",")
	if len(fds) != 5 {
		t.Fatalf("unexpected inherited descriptor list: %q", fds)
	}
	for i, raw := range fds {
		fd, err := strconv.Atoi(raw)
		if err != nil || fd != i+3 {
			t.Fatalf("invalid inherited descriptor %q", raw)
		}
	}
	rpc, live, cancel, handshake, err := OpenWorkerPipes(fds)
	if err != nil {
		t.Fatal(err)
	}
	var nonce [4]byte
	if _, err := io.ReadFull(handshake, nonce[:]); err != nil || string(nonce[:]) != "test" {
		t.Fatalf("startup handshake = %q, err = %v", nonce, err)
	}
	if mode == "exit17" {
		os.Exit(17)
	}
	defer func() { _ = rpc.Close() }()
	defer func() { _ = live.Close() }()
	defer func() { _ = cancel.Close() }()
	switch mode {
	case "eof":
		if _, err := io.Copy(io.Discard, live); err != nil {
			t.Fatal(err)
		}
	case "cancel":
		var signal [1]byte
		if _, err := io.ReadFull(cancel, signal[:]); err != nil || signal[0] != 1 {
			t.Fatalf("cancel signal = %v, err = %v", signal, err)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func waitWorkerExit(t *testing.T, worker *Worker) {
	t.Helper()
	select {
	case <-worker.done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not exit after cancellation")
	}
}

func closeTestFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}

func TestOpenWorkerPipesRejectsInvalidDescriptorArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"3", "bad", "5", "6", "7"}, {"2", "4", "5", "6", "7"}, {"999999", "4", "5", "6", "7"}} {
		if _, _, _, _, err := OpenWorkerPipes(args); err == nil {
			t.Errorf("OpenWorkerPipes(%v) unexpectedly succeeded", args)
		}
	}
}
