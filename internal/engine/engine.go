package engine

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/facts"
	"fmt"
	"os"
	"time"

	"github.com/dop251/goja"
)

type Engine struct {
	vm       *goja.Runtime
	facts    *facts.Facts
	rootPath string
}

// testInitHook is called at the end of New() during tests to inject custom init behavior.
// It must be nil in production; tests set it to simulate failure scenarios.
var testInitHook func(*Engine)

func New(binaryCommandOverride string) (e *Engine, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("engine initialization panic: %v", r)
		}
	}()

	// Collect facts about the environment
	projectFacts, gitRoot, err := facts.Collect(context.Background(), binaryCommandOverride)
	if err != nil {
		return nil, err
	}

	rootPath, err := computeRootPath(gitRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to compute root path: %w", err)
	}

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	e = &Engine{
		vm:       vm,
		facts:    projectFacts,
		rootPath: rootPath,
	}

	e.initConsole()
	e.initColors()
	e.initFormats()
	e.initTools()
	e.initFacts()

	if testInitHook != nil {
		testInitHook(e)
	}

	return e, nil
}

func (e *Engine) VM() *goja.Runtime {
	return e.vm
}

func (e *Engine) Facts() *facts.Facts {
	return e.facts
}

// withTimeout arms a watchdog goroutine that interrupts the VM after timeout.
// The returned done() function signals the watchdog to stop and blocks until it
// has exited, so callers can safely call e.vm.ClearInterrupt() after done()
// returns without a race against a late-firing interrupt.
// Only vm.Interrupt is called from the goroutine, which goja guarantees is safe
// to call from a different goroutine.
func (e *Engine) withTimeout(timeout time.Duration) (done func()) {
	doneCh := make(chan struct{})
	exitCh := make(chan struct{})
	go func() {
		defer close(exitCh)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			e.vm.Interrupt("execution timeout")
		case <-doneCh:
		}
	}()
	return func() {
		close(doneCh)
		<-exitCh
	}
}

// RunWithTimeout executes a JS script string with a watchdog timeout.
func (e *Engine) RunWithTimeout(script string, timeout time.Duration) (goja.Value, error) {
	done := e.withTimeout(timeout)
	val, err := e.vm.RunString(script)
	done()
	e.vm.ClearInterrupt()
	return val, err
}

// CallWithTimeout invokes a goja.Callable with a watchdog timeout.
func (e *Engine) CallWithTimeout(fn goja.Callable, timeout time.Duration, args ...goja.Value) (goja.Value, error) {
	done := e.withTimeout(timeout)
	val, err := fn(goja.Undefined(), args...)
	done()
	e.vm.ClearInterrupt()
	return val, err
}

// initFacts exposes the facts() function to JavaScript
func (e *Engine) initFacts() {
	_ = e.vm.Set("facts", func() goja.Value {
		return e.vm.ToValue(e.facts)
	})
}

// computeRootPath returns git root if non-empty, otherwise cwd.
func computeRootPath(gitRoot string) (string, error) {
	if gitRoot != "" {
		return gitRoot, nil
	}
	return os.Getwd()
}
