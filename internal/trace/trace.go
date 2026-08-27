// Package trace records a high-resolution execution trace of one datamitsu
// invocation: nested spans with wall-clock offsets, monotonic durations and
// free-form counters, written out as a Chrome Trace Event file plus a
// human-readable report.
//
// It answers a different question than internal/timing. timing reports a fixed
// set of coarse stages to the terminal for a user watching a run; trace records
// everything, keeps per-goroutine ordering, and persists to disk so two runs can
// be diffed after the fact. Both can be on at once and neither reads the other.
//
// # Cost when disabled
//
// Tracing is off unless DATAMITSU_TRACE is set, and off costs one predictable
// branch:
//
//   - enabled is a package-level bool resolved once from the environment. Every
//     entry point tests it before touching a clock, a lock or the heap.
//   - Start returns a by-value Span whose zero value means "not recording", so
//     the disabled path allocates nothing and End is a single integer compare.
//     The compiler can fold both away.
//   - Counter.Add is one atomic increment on a pre-registered struct — no map
//     lookup, no string hashing — so it is safe to call from a per-file loop.
//
// Building with -tags datamitsu_notrace flips compiledIn to a false constant,
// which lets the compiler eliminate every recording body outright. That is the
// only mechanism that removes the code rather than skipping it; an -ldflags -X
// value cannot, because a linker-injected variable is a variable, not a
// constant, and the compiler must assume it may be true.
//
// # Naming
//
// Span names must be compile-time constants on hot paths. A name built with
// fmt.Sprintf allocates before Start is even called, so the disabled guard
// cannot save it — guard those call sites with Enabled() instead.
package trace

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Category groups spans in the trace viewer and in the text report.
const (
	CatConfig  = "config"
	CatEngine  = "engine"
	CatPlan    = "plan"
	CatWalk    = "walk"
	CatExec    = "exec"
	CatCache   = "cache"
	CatInstall = "install"
	CatParse   = "parse"
	CatCLI     = "cli"
)

// wallStart and monoStart pin the process start once, so every recorded offset
// is relative to the same origin even across goroutines.
var (
	wallStart = time.Now()
	monoStart = wallStart

	enabled atomic.Bool

	// command is the cobra command path of this invocation, used in the output
	// file name. Set by Init.
	command atomic.Pointer[string]
)

// Enabled reports whether spans and counters are being recorded. Hot paths that
// need a dynamically built span name must test this before building the name.
func Enabled() bool { return compiledIn && enabled.Load() }

// SetEnabled turns recording on or off. Init calls it from the resolved
// environment; tests call it directly.
func SetEnabled(on bool) {
	if !compiledIn {
		return
	}
	enabled.Store(on)
}

// nowNanos returns nanoseconds since process start. time.Since uses the
// monotonic clock embedded in wallStart, so a wall-clock adjustment mid-run
// cannot produce a negative duration.
func nowNanos() int64 { return int64(time.Since(monoStart)) }

// Attr is one key/value pair attached to a span or instant event. Values are
// rendered with %v, so any type is accepted; keep them small and allocation-free
// on hot paths (a string constant or an int).
type Attr struct {
	Key   string
	Value any
}

// A returns an Attr. It exists so call sites read as
// trace.A("files", n) rather than repeating the struct literal.
func A(key string, value any) Attr { return Attr{Key: key, Value: value} }

// Span is an open measurement. Its zero value records nothing, which is what
// Start returns when tracing is off — End on it is a single compare.
//
// Span is deliberately a value type: returning it by value keeps the disabled
// path off the heap. Copying one is harmless; ending the same span twice records
// it twice.
type Span struct {
	name  string
	cat   string
	start int64 // nanoseconds since process start; 0 means "not recording"
}

// Start opens a span in the given category. The returned Span must be ended:
//
//	defer trace.Start(trace.CatPlan, "collectTasks").End()
func Start(cat, name string) Span {
	if !compiledIn || !enabled.Load() {
		return Span{}
	}
	return Span{name: name, cat: cat, start: nowNanos()}
}

// End closes the span and records it.
func (s Span) End() { s.end(nil) }

// EndWith closes the span and records it with attributes describing what the
// span actually did — the file count it produced, the exit code it observed.
func (s Span) EndWith(attrs ...Attr) { s.end(attrs) }

func (s Span) end(attrs []Attr) {
	if s.start == 0 {
		return
	}
	rec.addSpan(event{
		Name:  s.name,
		Cat:   s.cat,
		Start: s.start,
		Dur:   nowNanos() - s.start,
		Attrs: attrs,
	})
}

// Instant records a zero-duration event — something that happened rather than
// something that took time (a cache miss, a fallback engaging).
func Instant(cat, name string, attrs ...Attr) {
	if !compiledIn || !enabled.Load() {
		return
	}
	rec.addSpan(event{
		Name:    name,
		Cat:     cat,
		Start:   nowNanos(),
		Dur:     -1, // marks an instant; the sink emits ph:"i"
		Attrs:   attrs,
		Instant: true,
	})
}

// Counter is a pre-registered monotonic counter. Register it once at package
// level and call Add from the hot loop:
//
//	var globMatches = trace.NewCounter("plan.glob.match_calls")
//	globMatches.Add(1)
//
// Add is one atomic increment when tracing is on and one branch when it is off,
// so a counter is affordable where a span is not.
type Counter struct {
	name string
	n    atomic.Int64
}

// NewCounter registers a counter. Call it from a package-level var so the
// registration happens once, at init, rather than on the measured path.
func NewCounter(name string) *Counter {
	c := &Counter{name: name}
	countersMu.Lock()
	counters = append(counters, c)
	countersMu.Unlock()
	return c
}

// Add increases the counter by delta.
func (c *Counter) Add(delta int64) {
	if !compiledIn || !enabled.Load() {
		return
	}
	c.n.Add(delta)
}

// Value returns the counter's current value.
func (c *Counter) Value() int64 { return c.n.Load() }

// Name returns the counter's registered name.
func (c *Counter) Name() string { return c.name }

var (
	countersMu sync.Mutex
	counters   []*Counter
)

// Counters returns every registered counter, in registration order.
func Counters() []*Counter {
	countersMu.Lock()
	defer countersMu.Unlock()
	return append([]*Counter(nil), counters...)
}

// event is one recorded span or instant.
type event struct {
	Name    string
	Cat     string
	Start   int64 // ns since process start
	Dur     int64 // ns; -1 for an instant
	Attrs   []Attr
	Instant bool
}

// recorder accumulates events for the process.
//
// A single mutex is enough: spans are coarse by construction (per config source,
// per tool, per spawned process — never per file), so contention is far below
// the cost of the work each span wraps. Counters carry the per-file volume and
// never touch this lock.
type recorder struct {
	mu     sync.Mutex
	events []event
	// dropped counts events discarded after maxEvents, so a runaway
	// instrumentation bug reports itself instead of exhausting memory.
	dropped int64
}

// maxEvents caps memory for a pathological run. 200k events is ~20 MB and far
// beyond any correctly instrumented invocation.
const maxEvents = 200_000

var rec = &recorder{}

func (r *recorder) addSpan(e event) {
	r.mu.Lock()
	if len(r.events) >= maxEvents {
		r.dropped++
		r.mu.Unlock()
		return
	}
	r.events = append(r.events, e)
	r.mu.Unlock()
}

// snapshot returns a copy of the recorded events and the dropped count.
func (r *recorder) snapshot() ([]event, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event(nil), r.events...), r.dropped
}

// Reset drops every recorded event and zeroes every counter. Tests use it to
// isolate cases; production code never calls it.
func Reset() {
	rec.mu.Lock()
	rec.events = nil
	rec.dropped = 0
	rec.mu.Unlock()

	countersMu.Lock()
	for _, c := range counters {
		c.n.Store(0)
	}
	countersMu.Unlock()
}

// Init resolves whether tracing is on and records the command path for the
// output file name. It is safe to call more than once; the last call wins.
//
// on is passed in rather than read here so the package keeps no dependency on
// internal/env, which would be an import cycle for anything env itself traces.
func Init(on bool, commandPath string) {
	SetEnabled(on)
	c := commandPath
	command.Store(&c)
}

// commandName returns the recorded command path, or "datamitsu" when Init has
// not run (a direct library use, or a failure before flag parsing).
func commandName() string {
	if p := command.Load(); p != nil && *p != "" {
		return *p
	}
	if len(os.Args) > 1 {
		return os.Args[1]
	}
	return "datamitsu"
}
