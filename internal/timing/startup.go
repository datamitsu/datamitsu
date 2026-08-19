package timing

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

// Startup phase names. They are constants rather than free-form strings so the
// benchmark/report tooling and the tests assert on the same vocabulary.
const (
	// PhaseLoadConfig covers one whole loadConfigImpl call.
	PhaseLoadConfig = "loadConfig"
	// PhaseGitRoot covers one facts.GetGitRoot lookup made by the config loader.
	PhaseGitRoot = "facts.GetGitRoot"
	// PhaseDiscoverBeforeConfigs covers the getBeforeConfigs() pre-pass, which
	// builds an entire engine of its own.
	PhaseDiscoverBeforeConfigs = "discoverBeforeConfigs"
	// PhaseEngineNew covers one engine.NewWithOptions call.
	PhaseEngineNew = "engine.New"
	// PhaseStripTypes covers one config.StripTypes (esbuild) call.
	PhaseStripTypes = "config.StripTypes"
	// PhaseGetConfig covers one getConfig() evaluation in the goja VM.
	PhaseGetConfig = "getConfig"
)

// StartupPhase is a single recorded startup measurement. Unlike Stage it is
// flat: startup phases are recorded from several packages that do not share a
// parent handle, so they are aggregated by name instead of nested.
type StartupPhase struct {
	Name    string
	Count   int
	Total   time.Duration
	Longest time.Duration
}

// startupRecorder aggregates startup phase durations for the process.
type startupRecorder struct {
	mu      sync.Mutex
	phases  map[string]*StartupPhase
	order   []string
	printed bool
}

var startup = &startupRecorder{phases: make(map[string]*StartupPhase)}

// startupEnabled reports whether startup instrumentation is active. The env var
// is read on every call rather than cached so tests can toggle it with
// t.Setenv; the cost is one os.Getenv on a path that runs a handful of times
// per process.
func startupEnabled() bool {
	return env.IsStartupTimingsEnabled()
}

// StartStartupPhase begins timing a startup phase and returns the function that
// ends it. When startup instrumentation is disabled the returned function is a
// no-op and nothing is recorded.
//
// Usage: defer timing.StartStartupPhase(timing.PhaseEngineNew)()
func StartStartupPhase(name string) func() {
	if !startupEnabled() {
		return func() {}
	}

	start := time.Now()
	return func() {
		startup.record(name, time.Since(start))
	}
}

// record adds one observation of a phase.
func (r *startupRecorder) record(name string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.phases[name]
	if !ok {
		p = &StartupPhase{Name: name}
		r.phases[name] = p
		r.order = append(r.order, name)
	}
	p.Count++
	p.Total += d
	if d > p.Longest {
		p.Longest = d
	}
}

// StartupPhases returns a copy of the recorded phases in first-recorded order.
func StartupPhases() []StartupPhase {
	startup.mu.Lock()
	defer startup.mu.Unlock()

	out := make([]StartupPhase, 0, len(startup.order))
	for _, name := range startup.order {
		out = append(out, *startup.phases[name])
	}
	return out
}

// ResetStartupPhases drops every recorded phase. Tests use it to isolate cases;
// production code never calls it.
func ResetStartupPhases() {
	startup.mu.Lock()
	defer startup.mu.Unlock()

	startup.phases = make(map[string]*StartupPhase)
	startup.order = nil
	startup.printed = false
}

// PrintStartup writes the recorded startup phases to w, slowest total first. It
// writes nothing when instrumentation is disabled, when nothing was recorded,
// or when a previous call already printed. Every phase is recorded inside the
// config load, which is where the single call site lives; the print-once guard
// keeps a command that loads config twice from emitting two reports.
func PrintStartup(w io.Writer) {
	if !startupEnabled() {
		return
	}

	phases := StartupPhases()
	if len(phases) == 0 {
		return
	}

	startup.mu.Lock()
	alreadyPrinted := startup.printed
	startup.printed = true
	startup.mu.Unlock()
	if alreadyPrinted {
		return
	}

	sort.SliceStable(phases, func(a, b int) bool {
		return phases[a].Total > phases[b].Total
	})

	_, _ = fmt.Fprintln(w, "⏱  Startup phases")
	for _, p := range phases {
		_, _ = fmt.Fprintf(w, "  %-24s n=%-3d total=%-10s max=%s\n",
			p.Name, p.Count, formatStartupDuration(p.Total), formatStartupDuration(p.Longest))
	}
}

// formatStartupDuration keeps sub-millisecond resolution, which formatDuration
// (whole milliseconds) throws away — most startup phases are microseconds once
// the optimisation work lands.
func formatStartupDuration(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1e6)
	default:
		return fmt.Sprintf("%.3fs", d.Seconds())
	}
}
