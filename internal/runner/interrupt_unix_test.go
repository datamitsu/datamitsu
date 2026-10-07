//go:build !windows

package runner

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// The first SIGINT cancels the run's context with an interruption that names
// it and exits 130; the process itself survives to report what it stopped.
func TestNotifyInterruptCancelsOnSignal(t *testing.T) {
	ctx, stop := notifyInterrupt(context.Background())
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context was not cancelled by SIGINT")
	}
	err := interruption(ctx)
	if err == nil {
		t.Fatalf("interruption() = nil, cause %v", context.Cause(ctx))
	}
	coded, ok := err.(interface{ ExitCode() int })
	if !ok || coded.ExitCode() != 130 {
		t.Errorf("interruption() = %v, want an error that exits 130", err)
	}
}

// Stopping the handler without a signal leaves no interruption behind.
func TestNotifyInterruptStopWithoutSignal(t *testing.T) {
	ctx, stop := notifyInterrupt(context.Background())
	stop()
	if ctx.Err() == nil {
		t.Error("the context is still live after stop")
	}
	if err := interruption(ctx); err != nil {
		t.Errorf("interruption() = %v after a plain stop, want nil", err)
	}
}
