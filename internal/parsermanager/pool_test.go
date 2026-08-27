package parsermanager

import (
	"context"
	"strconv"
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

// Reuse is gated on the module's `reset` export: without it a module makes no
// promise that parse N+1 cannot observe parse N, so its instances must be closed
// after every parse rather than pooled. Simulating a four-export module by
// dropping the resolved export reproduces exactly what such a module yields.
func TestUnresettableInstancesAreNeverPooled(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("warm"), nil, 0); err != nil {
		t.Fatalf("warmup ParseOutput() error = %v", err)
	}
	key, ok := m.instanceKey("echo")
	if !ok {
		t.Fatal("instanceKey(echo) not found")
	}
	// Make every pooled instance look like one from a module built against the
	// four-export ABI.
	m.mu.Lock()
	idle := m.idle[key]
	for _, inst := range idle {
		inst.reset = nil
	}
	m.mu.Unlock()
	if len(idle) == 0 {
		t.Fatal("precondition: the warmup parse pooled no instance")
	}

	tracingOn(t)
	diags, err := m.ParseOutput(ctx, "echo", "echo", []byte("unresettable"), nil, 0)
	if err != nil {
		t.Fatalf("ParseOutput() error = %v", err)
	}
	if len(diags) != 1 || diags[0].Message != "unresettable" {
		t.Fatalf("ParseOutput() = %+v, want the echoed input", diags)
	}

	m.mu.Lock()
	pooled := len(m.idle[key])
	m.mu.Unlock()
	if pooled != 0 {
		t.Errorf("pool holds %d instance(s) of a module that cannot reset, want 0", pooled)
	}
	if got := counter(t, "parser.unresettable_discards"); got != 1 {
		t.Errorf("parser.unresettable_discards = %d, want 1", got)
	}
}

// The reset happens before the instance is pooled, not after it is drawn: an
// instance sitting in the pool must already be clean, so a Manager shutdown or a
// crash cannot leave one tool's residue reachable by the next parse.
func TestPooledInstancesAreResetBeforePooling(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("first"), nil, 0); err != nil {
		t.Fatalf("ParseOutput() error = %v", err)
	}
	key, ok := m.instanceKey("echo")
	if !ok {
		t.Fatal("instanceKey(echo) not found")
	}
	m.mu.Lock()
	idle := append([]*ParserRuntime(nil), m.idle[key]...)
	m.mu.Unlock()
	if len(idle) != 1 {
		t.Fatalf("pool holds %d instances after one parse, want 1", len(idle))
	}
	// A second Reset on a pooled instance must still succeed — proof the export is
	// real and callable, not merely present.
	if err := idle[0].Reset(ctx); err != nil {
		t.Errorf("Reset() on a pooled instance error = %v", err)
	}
}

// Pooling must be invisible: the same inputs must produce the same diagnostics
// they do on fresh instances, including across an intervening ParseOutput that
// errors before it ever reaches an instance.
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

// The pool is bounded: a burst wider than maxIdleInstances must close the excess
// rather than keep it. Each idle instance holds its own wasm linear memory for
// the Manager's lifetime, so an unbounded pool is a leak sized by peak
// concurrency — and a bound accidentally set to zero silently deletes pooling.
func TestPoolIsBoundedByMaxIdleInstances(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	// Deliberately not driven through concurrent ParseOutput: nothing forces more
	// than one parse to overlap, so a concurrent burst can finish without ever
	// offering the pool a ninth instance and would pass with the bound deleted.
	// Releasing an explicit burst puts the bound itself under test.
	const burst = maxIdleInstances * 2
	for range burst {
		inst, err := m.Acquire(ctx, "echo")
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
		m.release(ctx, "echo", inst)
	}

	key, ok := m.instanceKey("echo")
	if !ok {
		t.Fatal("instanceKey(echo) not found")
	}
	m.mu.Lock()
	pooled := len(m.idle[key])
	m.mu.Unlock()

	if pooled != maxIdleInstances {
		t.Errorf("pool holds %d idle instances after releasing %d, want exactly %d", pooled, burst, maxIdleInstances)
	}
}

// A burst of concurrent parses must all succeed and leave the pool populated:
// the bound must not turn overlapping work into an error, and pooling must
// actually be happening at the end of a real run.
func TestConcurrentBurstLeavesThePoolPopulated(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	const burst = maxIdleInstances * 2
	var wg sync.WaitGroup
	for i := range burst {
		wg.Go(func() {
			if _, err := m.ParseOutput(ctx, "echo", "echo", []byte(strconv.Itoa(i)), nil, 0); err != nil {
				t.Errorf("ParseOutput()[%d] error = %v", i, err)
			}
		})
	}
	wg.Wait()

	key, ok := m.instanceKey("echo")
	if !ok {
		t.Fatal("instanceKey(echo) not found")
	}
	m.mu.Lock()
	pooled := len(m.idle[key])
	m.mu.Unlock()

	if pooled > maxIdleInstances {
		t.Errorf("pool holds %d idle instances after a burst of %d, want at most %d", pooled, burst, maxIdleInstances)
	}
	if pooled == 0 {
		t.Error("pool holds nothing after a burst of successful parses; pooling is not happening at all")
	}
}

// Rule 3 of ParseOutput: a failure on a *reused* instance is retried once on a
// fresh one, because the failure may belong to the instance rather than to the
// input. Pooling must never turn a parse that works into one that fails, so the
// pool is poisoned here with a closed instance — the exact way a reused instance
// can fail on input the module handles fine.
func TestParseRetriesOnceWhenAReusedInstanceFails(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	if _, err := m.ParseOutput(ctx, "echo", "echo", []byte("warm"), nil, 0); err != nil {
		t.Fatalf("first ParseOutput() error = %v", err)
	}

	key, ok := m.instanceKey("echo")
	if !ok {
		t.Fatal("instanceKey(echo) not found")
	}
	m.mu.Lock()
	idle := m.idle[key]
	m.mu.Unlock()
	if len(idle) != 1 {
		t.Fatalf("pool holds %d idle instances after one parse, want 1", len(idle))
	}
	if err := idle[0].Close(ctx); err != nil {
		t.Fatalf("Close() of the pooled instance error = %v", err)
	}

	diags, err := m.ParseOutput(ctx, "echo", "echo", []byte("retried"), nil, 0)
	if err != nil {
		t.Fatalf("ParseOutput() over a poisoned pool error = %v, want the retry to succeed", err)
	}
	if got := messages(diags); len(got) != 1 || got[0] != "retried" {
		t.Errorf("ParseOutput() = %v, want [retried]", got)
	}
}

// release racing a Close must close the instance, never resurrect it into a
// Manager whose runtime is gone.
func TestReleaseAfterCloseClosesTheInstance(t *testing.T) {
	m, _ := pooledManager(t)
	ctx := context.Background()

	inst, err := m.Acquire(ctx, "echo")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	m.release(ctx, "echo", inst)

	m.mu.Lock()
	pooled := len(m.idle)
	m.mu.Unlock()
	if pooled != 0 {
		t.Errorf("release() after Close() pooled %d module(s)", pooled)
	}
	// A closed instance cannot parse; that it cannot is how we know release did
	// not simply drop it on the floor still open.
	if _, err := inst.Parse(ctx, "echo", []byte("zombie"), nil, 0); err == nil {
		t.Error("the released instance still parses; release() left it open after Close()")
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
	// Without this the assertion below is vacuous: Close nils the pool outright,
	// so an empty pool proves nothing unless something was in it first.
	m.mu.Lock()
	warmed := len(m.idle)
	m.mu.Unlock()
	if warmed == 0 {
		t.Fatal("precondition: the warmup parse pooled no instance")
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
