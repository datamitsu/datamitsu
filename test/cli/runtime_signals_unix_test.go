//go:build linux

package cli_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/datamitsu/datamitsu/internal/clitest"
	"golang.org/x/sys/unix"
)

func TestRuntimeRunUnixSignalHandoff(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable")
	}
	p := clitest.NewProject(t)
	cfg := p.WriteFile("signals.config.js", sprintfRuntimeConfig(sh, sh))

	t.Run("ctrl-c reaches the interpreter", func(t *testing.T) {
		marker := filepath.Join(p.Dir, "interrupt-ready")
		master, slave := openRuntimePTY(t)
		cmd := exec.Command(clitest.BuildOnce(t), runtimeArgs(p, cfg, "run", "zed", "--", "-c", "touch "+shellQuoteRuntime(marker)+"; exec sleep 30")...)
		cmd.Dir, cmd.Env = p.Dir, clitest.BaseEnv(t.TempDir())
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		_ = slave.Close()
		defer func() { _ = unix.Close(master) }()
		waitForRuntimeFile(t, marker)
		if _, err := unix.Write(master, []byte{3}); err != nil {
			t.Fatalf("send terminal interrupt: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			status := &exec.ExitError{}
			ok := errors.As(err, &status)
			if !ok || status.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGINT {
				t.Fatalf("runtime run status = %v, want SIGINT", err)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatal("runtime run did not stop after terminal interrupt")
		}
	})

	t.Run("SIGTERM reaches the interpreter", func(t *testing.T) {
		marker := filepath.Join(p.Dir, "term-ready")
		master, slave := openRuntimePTY(t)
		cmd := exec.Command(clitest.BuildOnce(t), runtimeArgs(p, cfg, "run", "zed", "--", "-c", "touch "+shellQuoteRuntime(marker)+"; exec sleep 30")...)
		cmd.Dir, cmd.Env = p.Dir, clitest.BaseEnv(t.TempDir())
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		_ = slave.Close()
		defer func() { _ = unix.Close(master) }()
		waitForRuntimeFile(t, marker)
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		err = cmd.Wait()
		status := &exec.ExitError{}
		ok := errors.As(err, &status)
		if !ok || status.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Fatalf("runtime run status = %v, want SIGTERM", err)
		}
	})

	t.Run("ctrl-z and fg in invoking shell", func(t *testing.T) {
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("bash is unavailable")
		}
		marker := filepath.Join(p.Dir, "continued")
		ready := marker + ".ready"
		program := "touch " + shellQuoteRuntime(ready) + "; trap 'echo continued > " + shellQuoteRuntime(marker) + "; exit 0' CONT; while :; do sleep 1; done"
		cliArgs := runtimeArgs(p, cfg, "run", "zed", "--", "-c", program)
		quoted := make([]string, len(cliArgs)+1)
		quoted[0] = shellQuoteRuntime(clitest.BuildOnce(t))
		for i, arg := range cliArgs {
			quoted[i+1] = shellQuoteRuntime(arg)
		}
		master, slave := openRuntimePTY(t)
		cmd := exec.Command(bash, "--noprofile", "--norc", "-i")
		cmd.Dir, cmd.Env = p.Dir, clitest.BaseEnv(t.TempDir())
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		_ = slave.Close()
		done := false
		defer func() {
			if !done {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			_ = unix.Close(master)
		}()
		if _, err := unix.Write(master, []byte(strings.Join(quoted, " ")+"\n")); err != nil {
			t.Fatal(err)
		}
		waitForRuntimeFile(t, ready)
		if _, err := unix.Write(master, []byte{26}); err != nil {
			t.Fatalf("send terminal suspend: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
		if _, err := unix.Write(master, []byte("fg\n")); err != nil {
			t.Fatalf("resume foreground job: %v", err)
		}
		waitForRuntimeFile(t, marker)
		if _, err := unix.Write(master, []byte("exit\n")); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); clitest.ExitCodeOf(err) != 0 {
			t.Fatalf("invoking shell after fg: %v", err)
		}
		done = true
	})
}

func waitForRuntimeFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime child did not create %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func openRuntimePTY(t *testing.T) (int, *os.File) {
	t.Helper()
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open PTY master: %v", err)
	}
	var unlock int32
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = unix.Close(master)
		t.Fatalf("unlock PTY: %v", errno)
	}
	var number uint32
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
		_ = unix.Close(master)
		t.Fatalf("read PTY number: %v", errno)
	}
	slaveFD, err := unix.Open(filepath.Join("/dev/pts", strconv.Itoa(int(number))), unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = unix.Close(master)
		t.Fatalf("open PTY slave: %v", err)
	}
	return master, os.NewFile(uintptr(slaveFD), "runtime-pty-slave")
}

func shellQuoteRuntime(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
