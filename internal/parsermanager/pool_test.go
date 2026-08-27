package parsermanager

import (
	"context"
	"sync"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/trace"
)

// pooledManager serves the echo fixture from a local server, so every case here
// exercises the real download → verify → compile → instantiate path.
func pooledManager(t *testing.T) (*Manager, []byte) {
	t.Helper()
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	wasm := echoWASM(t)
	srv, _ := serveWASM(t, wasm)
	m := New(config.MapOfParsers{"echo": {URL: srv.URL, Hash: sha256Hex(wasm)}})
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	return m, wasm
}

// messages flattens diagnostics to the part a caller compares.
func messages(diags []RawDiagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Message)
	}
	return out
}

func tracingOn(t *testing.T) {
	t.Helper()
	prev := trace.Enabled()
	trace.Reset()
	trace.SetEnabled(true)
	t.Cleanup(func() {
		trace.SetEnabled(prev)
		trace.Reset()
	})
}

func counter(t *testing.T, name string) int64 {
	t.Helper()
	for _, c := range trace.Counters() {
		if c.Name() == name {
			return c.Value()
		}
	}
	t.Fatalf("counter %q is not registered", name)
	return 0
}

// Sequential parses of one module must reuse one instance: instantiation is
// ~230 µs of pure overhead per parsed tool invocation, and a run parses one per
// tool per unit.
func TestParseOutputReusesOneInstance(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	// Warm the download/compile outside the counted window.
	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("warm"), nil, 0); err != nil {
		t.Fatalf("warmup ParseOutput() error = %v", err)
	}

	tracingOn(t)
	for range 5 {
		if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("x"), nil, 0); err != nil {
			t.Fatalf("ParseOutput() error = %v", err)
		}
	}

	if got := counter(t, "parser.module_instantiations"); got != 0 {
		t.Errorf("parser.module_instantiations = %d over 5 sequential parses, want 0 (all pooled)", got)
	}
	if got := counter(t, "parser.instance_pool_hits"); got != 5 {
		t.Errorf("parser.instance_pool_hits = %d, want 5", got)
	}
}

// Pooling must be invisible: the same inputs must produce the same diagnostics
// they do on fresh instances — including across a parse that fails in between,
// which is the case where a reused instance could carry state forward.
func TestPooledParsesMatchFreshInstances(t *testing.T) {
	m, wasm := pooledManager(t)
	ctx := context.Background()

	inputs := [][]byte{[]byte("first line\nsecond line"), []byte("another")}

	// Baseline: two parses, each on its own fresh instance.
	want := make([][]string, 0, len(inputs))
	for _, in := range inputs {
		rt, err := NewRuntime(ctx, wasm)
		if err != nil {
			t.Fatalf("NewRuntime() error = %v", err)
		}
		diags, err := rt.Parse(ctx, "echo", in, nil, 0)
		if err != nil {
			t.Fatalf("fresh Parse() error = %v", err)
		}
		_ = rt.Close(ctx)
		want = append(want, messages(diags))
	}

	got, err := m.ParseOutput(ctx, "echo", "echo", inputs[0], nil, 0)
	if err != nil {
		t.Fatalf("first pooled ParseOutput() error = %v", err)
	}
	if !equalStrings(messages(got), want[0]) {
		t.Errorf("first pooled parse = %v, want %v", messages(got), want[0])
	}

	// A parse that fails in between: an undeclared module never reaches the pool,
	// so the pooled instance from the first parse must still be intact.
	if _, err := m.ParseOutput(ctx, "not-declared", "echo", inputs[0], nil, 0); err == nil {
		t.Fatal("ParseOutput(not-declared) error = nil, want an error")
	}

	got, err = m.ParseOutput(ctx, "echo", "echo", inputs[1], nil, 0)
	if err != nil {
		t.Fatalf("second pooled ParseOutput() error = %v", err)
	}
	if !equalStrings(messages(got), want[1]) {
		t.Errorf("second pooled parse = %v, want %v", messages(got), want[1])
	}
}

// The one failure mode pooling introduces: an instance that stops working while
// it sits idle. A parse that works on a fresh instance must not fail because it
// drew a dead one from the pool.
func TestPooledParseSurvivesADeadIdleInstance(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("warm"), nil, 0); err != nil {
		t.Fatalf("warmup ParseOutput() error = %v", err)
	}

	// Kill every idle instance underneath the pool.
	key, ok := m.instanceKey("echo")
	if !ok {
		t.Fatal("instanceKey(echo) not found")
	}
	m.mu.Lock()
	idle := m.idle[key]
	m.mu.Unlock()
	if len(idle) == 0 {
		t.Fatal("a finished parse did not return its instance to the pool")
	}
	for _, inst := range idle {
		_ = inst.Close(ctx)
	}

	diags, err := m.ParseOutput(ctx, "echo", "echo", []byte("after the corpse"), nil, 0)
	if err != nil {
		t.Fatalf("ParseOutput() after a dead pooled instance error = %v", err)
	}
	if len(diags) != 1 || diags[0].Message != "after the corpse" {
		t.Fatalf("ParseOutput() = %+v, want the echoed input", diags)
	}
}

// Concurrent parses each need their own linear memory; the pool must hand out
// distinct instances and never two callers the same one.
func TestPooledParsesAreSafeUnderConcurrency(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()
	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("warm"), nil, 0); err != nil {
		t.Fatalf("warmup ParseOutput() error = %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 16)
	msgs := make([]string, 16)
	for i := range 16 {
		wg.Go(func() {
			in := []byte(string(rune('a'+i)) + "-concurrent")
			diags, err := m.ParseOutput(ctx, "echo", "echo", in, nil, 0)
			errs[i] = err
			if err == nil && len(diags) == 1 {
				msgs[i] = diags[0].Message
			}
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent ParseOutput()[%d] error = %v", i, err)
		}
		if want := string(rune('a'+i)) + "-concurrent"; msgs[i] != want {
			t.Errorf("concurrent parse %d = %q, want %q", i, msgs[i], want)
		}
	}
}

// Close must leave nothing pooled: a release after Close would resurrect an
// instance into a Manager whose runtime is gone.
func TestCloseDropsPooledInstances(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()
	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("warm"), nil, 0); err != nil {
		t.Fatalf("warmup ParseOutput() error = %v", err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	m.mu.Lock()
	pooled := len(m.idle)
	m.mu.Unlock()
	if pooled != 0 {
		t.Errorf("Close() left %d pooled module(s)", pooled)
	}
	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("after close"), nil, 0); err == nil {
		t.Error("ParseOutput() after Close() error = nil, want the closed-manager error")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
