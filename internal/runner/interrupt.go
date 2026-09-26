package runner

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// interruptSignals are the signals that stop a run: Ctrl-C and a polite kill.
var interruptSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// interruptedError is the cause a run's context is cancelled with when a signal
// arrives, and the error the run then returns. It exits 128+signal, which is
// what a shell reported before the run caught the signal at all.
type interruptedError struct{ sig os.Signal }

func (e interruptedError) Error() string {
	return "interrupted by " + signalName(e.sig)
}

// ExitCode implements the interface cmd checks with errors.As.
func (e interruptedError) ExitCode() int {
	if s, ok := e.sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 1
}

func signalName(sig os.Signal) string {
	switch sig {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return sig.String()
}

// notifyInterrupt returns a context cancelled by the first SIGINT or SIGTERM.
//
// Tool processes run in their own process groups, so a terminal's Ctrl-C used
// to reach datamitsu alone and kill it, leaving its tools running and its event
// stream with starts that never ended. Catching the first signal lets the
// executor stop every tool and the run report what it did not finish. The
// handler is removed as soon as it fires, so a second Ctrl-C kills the process
// the way it always did.
func notifyInterrupt(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, interruptSignals...)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-signals:
			signal.Stop(signals)
			cancel(interruptedError{sig: sig})
		case <-done:
		}
	}()
	return ctx, func() {
		signal.Stop(signals)
		close(done)
		cancel(nil)
	}
}

// interruption returns the interruptedError ctx was cancelled with, if any.
func interruption(ctx context.Context) error {
	if interrupted, ok := errors.AsType[interruptedError](context.Cause(ctx)); ok {
		return interrupted
	}
	return nil
}
