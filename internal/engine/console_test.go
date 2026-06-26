package engine

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

type noopSink struct{}

func (noopSink) Emit(uievent.Event) {}

// captureStdout swaps os.Stdout for a pipe, runs fn, and returns what fn wrote.
// Engine tests run sequentially (no t.Parallel), so the swap is safe.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	if err := runtimeconfig.Init(); err != nil {
		t.Fatalf("runtimeconfig.Init: %v", err)
	}
	e, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return e
}

// In console mode a config's console.log reaches stdout (baseline — proves the
// next test's silence is real suppression, not a broken console).
func TestConsoleLogReachesStdoutInConsoleMode(t *testing.T) {
	ui.SetEventSink(nil, false)
	e := newEngine(t)

	out := captureStdout(t, func() {
		if _, err := e.vm.RunString(`console.log("MARKER-CONSOLE")`); err != nil {
			t.Fatalf("RunString: %v", err)
		}
	})
	if !strings.Contains(out, "MARKER-CONSOLE") {
		t.Errorf("console.log should reach stdout in console mode; got %q", out)
	}
}

// In JSON-L (quiet) mode console.log/info must NOT reach stdout — they would
// pollute the clean machine-data channel. (warn/error are diverted off stderr by
// the same ui.Quiet() guard.)
func TestConsoleSuppressedFromStdoutInJSONLMode(t *testing.T) {
	ui.SetEventSink(noopSink{}, true)
	defer ui.SetEventSink(nil, false)
	e := newEngine(t)

	out := captureStdout(t, func() {
		for _, expr := range []string{`console.log("M1")`, `console.info("M2")`} {
			if _, err := e.vm.RunString(expr); err != nil {
				t.Fatalf("RunString(%s): %v", expr, err)
			}
		}
	})
	if out != "" {
		t.Errorf("console.* must not reach stdout in JSON-L mode; leaked %q", out)
	}
}
