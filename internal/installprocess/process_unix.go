//go:build !windows

package installprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type process struct{ pid int }

func start(cmd *exec.Cmd) (*process, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start installer process: %w", err)
	}
	return &process{pid: cmd.Process.Pid}, nil
}

func finish(ctx context.Context, cmd *exec.Cmd, p *process) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var runErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		_ = syscall.Kill(-p.pid, syscall.SIGKILL)
		runErr = <-done
	}
	_ = syscall.Kill(-p.pid, syscall.SIGKILL)
	for groupAlive(p.pid) {
		if err := syscall.Kill(-p.pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && runErr == nil {
			runErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), runErr)
	}
	return runErr
}
