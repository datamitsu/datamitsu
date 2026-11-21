package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestNestedWatchdogAllowsReuseAfterInterruption(t *testing.T) {
	e := &Engine{facts: nil, rootPath: "", timeoutDepth: 0, nonDeterminism: "", sideEffect: "", vm: goja.New()}
	if err := e.vm.Set("nested", func() goja.Value {
		program := goja.MustCompile("nested.js", `while (true) {}`, false)
		value, err := e.RunProgramWithTimeout(program, 10*time.Millisecond)
		if interrupted, ok := errors.AsType[*goja.InterruptedError](err); ok {
			panic(interrupted)
		}
		if err != nil {
			panic(e.vm.NewGoError(err))
		}
		return value
	}); err != nil {
		t.Fatal(err)
	}
	_, err := e.RunWithTimeout(`try { nested(); } catch (_) {} "caught";`, time.Second)
	if _, ok := errors.AsType[*goja.InterruptedError](err); !ok {
		t.Fatalf("nested interruption did not escape JavaScript: %v", err)
	}
	value, err := e.RunWithTimeout(`21 * 2`, time.Second)
	if err != nil || value.ToInteger() != 42 {
		t.Fatalf("runtime unusable after interruption: %v, %v", value, err)
	}
}
