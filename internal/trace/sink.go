package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FlushOptions describes where a finished trace is written and how the file is
// named. Dir is resolved by the caller rather than here so this package keeps no
// dependency on the env/cache layout — which would make tracing those packages
// an import cycle.
type FlushOptions struct {
	// Dir receives the trace files. An empty Dir writes no files; the stderr
	// summary is still printed.
	Dir string
	// Slug identifies the traced repository in the file name (usually the git
	// root's base name). Empty falls back to "repo".
	Slug string
	// Summary receives the human-readable report. Nil suppresses it.
	Summary io.Writer
	// Meta is recorded verbatim in the trace file's otherData block.
	Meta map[string]string
}

// flushed guards against a double flush — Execute flushes on the normal path and
// again from the error path before os.Exit, and only the first should write.
var flushed bool

// Flush writes the recorded trace and returns the paths written. It is a no-op
// when tracing is disabled or already flushed.
//
// Two files are produced per run:
//
//	<stem>.json  Chrome Trace Event Format — open in Perfetto or chrome://tracing
//	<stem>.txt   the same data as an aggregated, sorted report
//
// Failing to write is reported to Summary and never returned as a fatal error:
// losing a trace must not change the exit status of the command being traced.
func Flush(opts FlushOptions) []string {
	if !compiledIn || !enabled.Load() {
		return nil
	}

	rec.mu.Lock()
	if flushed {
		rec.mu.Unlock()
		return nil
	}
	flushed = true
	rec.mu.Unlock()

	events, dropped := rec.snapshot()
	total := nowNanos()

	var written []string
	if opts.Dir != "" {
		stem, err := ensureStem(opts.Dir, opts.Slug)
		if err != nil {
			_, _ = fmt.Fprintf(errWriter(opts.Summary), "trace: cannot create %s: %v\n", opts.Dir, err)
		} else {
			if p, err := writeChrome(stem+".json", events, total, opts.Meta); err != nil {
				_, _ = fmt.Fprintf(errWriter(opts.Summary), "trace: %v\n", err)
			} else {
				written = append(written, p)
			}
			if p, err := writeReport(stem+".txt", events, total, dropped, opts.Meta); err != nil {
				_, _ = fmt.Fprintf(errWriter(opts.Summary), "trace: %v\n", err)
			} else {
				written = append(written, p)
			}
		}
	}

	if opts.Summary != nil {
		printSummary(opts.Summary, events, total, dropped)
		for _, p := range written {
			_, _ = fmt.Fprintf(opts.Summary, "   trace → %s\n", p)
		}
	}
	return written
}

func errWriter(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stderr
}

// ensureStem creates dir and returns the path prefix shared by this run's files.
// The name sorts chronologically, names the repository and the command so a
// directory of traces is readable without opening one, and carries the pid so
// two concurrent runs cannot collide.
func ensureStem(dir, slug string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating trace directory: %w", err)
	}
	if slug == "" {
		slug = "repo"
	}
	name := fmt.Sprintf("%s-%s-%s-%d",
		wallStart.UTC().Format("20060102T150405.000Z"),
		sanitize(slug),
		sanitize(commandName()),
		os.Getpid(),
	)
	return filepath.Join(dir, name), nil
}

// sanitize reduces a value to characters that are safe in a file name on every
// supported platform, so a command path ("config runtime") or a repository name
// with a slash cannot escape the trace directory.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "x"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// chromeEvent is one entry of the Chrome Trace Event Format array.
type chromeEvent struct {
	Name string         `json:"name"`
	Cat  string         `json:"cat,omitempty"`
	Ph   string         `json:"ph"`
	Ts   float64        `json:"ts"`
	Dur  float64        `json:"dur,omitempty"`
	Pid  int            `json:"pid"`
	Tid  int            `json:"tid"`
	S    string         `json:"s,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

type chromeFile struct {
	TraceEvents     []chromeEvent     `json:"traceEvents"`
	DisplayTimeUnit string            `json:"displayTimeUnit"`
	OtherData       map[string]string `json:"otherData,omitempty"`
}

// writeChrome emits the trace in Chrome Trace Event Format.
//
// Complete ("X") events must nest strictly within a thread, but datamitsu's
// spans come from many goroutines and overlap freely, so real thread ids would
// produce a file the viewers reject. Spans are packed into synthetic lanes
// instead — assignLanes gives each span the lowest lane where it either sits at
// top level or nests wholly inside that lane's open span — which is exactly the
// flame chart a reader wants, and is honest: the lane number is presentational,
// and the timestamps it is derived from are the measurement.
func writeChrome(path string, events []event, total int64, meta map[string]string) (string, error) {
	lanes := assignLanes(events)

	out := chromeFile{
		TraceEvents:     make([]chromeEvent, 0, len(events)+len(Counters())+1),
		DisplayTimeUnit: "ms",
		OtherData:       meta,
	}

	for i, e := range events {
		ce := chromeEvent{
			Name: e.Name,
			Cat:  e.Cat,
			Ts:   float64(e.Start) / 1000,
			Pid:  1,
			Tid:  lanes[i],
			Args: attrsToMap(e.Attrs),
		}
		if e.Instant {
			ce.Ph = "i"
			ce.S = "t"
		} else {
			ce.Ph = "X"
			ce.Dur = float64(e.Dur) / 1000
		}
		out.TraceEvents = append(out.TraceEvents, ce)
	}

	// Counters ride along as a single instant at the end of the run, so the
	// viewer shows them next to the spans that produced them.
	if args := countersToMap(); len(args) > 0 {
		out.TraceEvents = append(out.TraceEvents, chromeEvent{
			Name: "counters",
			Cat:  "counters",
			Ph:   "i",
			S:    "g",
			Ts:   float64(total) / 1000,
			Pid:  1,
			Tid:  0,
			Args: args,
		})
	}

	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encoding trace: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

func attrsToMap(attrs []Attr) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		m[a.Key] = a.Value
	}
	return m
}

func countersToMap() map[string]any {
	var m map[string]any
	for _, c := range Counters() {
		v := c.Value()
		if v == 0 {
			continue
		}
		if m == nil {
			m = make(map[string]any)
		}
		m[c.Name()] = v
	}
	return m
}

// assignLanes returns a lane index per event such that within one lane every
// span is either disjoint from or wholly contained in the span above it.
func assignLanes(events []event) []int {
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	// Earlier start first; on a tie the longer span first, so a container is
	// placed before what it contains.
	sort.SliceStable(order, func(a, b int) bool {
		ea, eb := events[order[a]], events[order[b]]
		if ea.Start != eb.Start {
			return ea.Start < eb.Start
		}
		return spanEnd(ea) > spanEnd(eb)
	})

	lanes := make([]int, len(events))
	// stacks[l] holds the end times of the spans currently open in lane l,
	// outermost first.
	var stacks [][]int64

	for _, idx := range order {
		e := events[idx]
		start, end := e.Start, spanEnd(e)
		placed := false
		for l := range stacks {
			for len(stacks[l]) > 0 && stacks[l][len(stacks[l])-1] <= start {
				stacks[l] = stacks[l][:len(stacks[l])-1]
			}
			if len(stacks[l]) == 0 || end <= stacks[l][len(stacks[l])-1] {
				stacks[l] = append(stacks[l], end)
				lanes[idx] = l
				placed = true
				break
			}
		}
		if !placed {
			stacks = append(stacks, []int64{end})
			lanes[idx] = len(stacks) - 1
		}
	}
	return lanes
}

func spanEnd(e event) int64 {
	if e.Instant {
		return e.Start
	}
	return e.Start + e.Dur
}

// agg is one row of the aggregated report: every occurrence of a (category,
// name) pair collapsed together.
type agg struct {
	cat, name string
	count     int
	total     time.Duration
	max       time.Duration
	first     int64
}

func aggregate(events []event) []agg {
	index := make(map[string]int)
	var rows []agg
	for _, e := range events {
		if e.Instant {
			continue
		}
		key := e.Cat + "\x00" + e.Name
		i, ok := index[key]
		if !ok {
			rows = append(rows, agg{cat: e.Cat, name: e.Name, first: e.Start})
			i = len(rows) - 1
			index[key] = i
		}
		rows[i].count++
		rows[i].total += time.Duration(e.Dur)
		if d := time.Duration(e.Dur); d > rows[i].max {
			rows[i].max = d
		}
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].total > rows[b].total })
	return rows
}

func writeReport(path string, events []event, total, dropped int64, meta map[string]string) (string, error) {
	var b strings.Builder
	b.WriteString("datamitsu execution trace\n")
	b.WriteString(strings.Repeat("=", 78) + "\n")
	_, _ = fmt.Fprintf(&b, "started      %s\n", wallStart.Format(time.RFC3339Nano))
	_, _ = fmt.Fprintf(&b, "wall total   %s\n", dur(time.Duration(total)))
	_, _ = fmt.Fprintf(&b, "pid          %d\n", os.Getpid())
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(&b, "%-12s %s\n", k, meta[k])
	}
	if dropped > 0 {
		_, _ = fmt.Fprintf(&b, "dropped      %d events (cap %d reached)\n", dropped, maxEvents)
	}

	b.WriteString("\naggregated spans (by total time)\n")
	b.WriteString(strings.Repeat("-", 78) + "\n")
	_, _ = fmt.Fprintf(&b, "%-10s %-40s %6s %10s %10s\n", "category", "span", "n", "total", "max")
	for _, r := range aggregate(events) {
		_, _ = fmt.Fprintf(&b, "%-10s %-40s %6d %10s %10s\n",
			r.cat, truncate(r.name, 40), r.count, dur(r.total), dur(r.max))
	}

	b.WriteString("\ntimeline (nested by containment)\n")
	b.WriteString(strings.Repeat("-", 78) + "\n")
	writeTimeline(&b, events)

	if rows := counterRows(); len(rows) > 0 {
		b.WriteString("\ncounters\n")
		b.WriteString(strings.Repeat("-", 78) + "\n")
		for _, r := range rows {
			_, _ = fmt.Fprintf(&b, "%-52s %14d\n", r.name, r.value)
		}
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

type counterRow struct {
	name  string
	value int64
}

func counterRows() []counterRow {
	var rows []counterRow
	for _, c := range Counters() {
		if v := c.Value(); v != 0 {
			rows = append(rows, counterRow{c.Name(), v})
		}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].name < rows[b].name })
	return rows
}

// writeTimeline prints the spans as a tree, nesting a span under the last span
// that fully contains it. Instants are printed at the depth of their container.
func writeTimeline(w io.Writer, events []event) {
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ea, eb := events[order[a]], events[order[b]]
		if ea.Start != eb.Start {
			return ea.Start < eb.Start
		}
		return spanEnd(ea) > spanEnd(eb)
	})

	var stack []int64 // end times of open ancestors
	for _, idx := range order {
		e := events[idx]
		for len(stack) > 0 && stack[len(stack)-1] <= e.Start {
			stack = stack[:len(stack)-1]
		}
		depth := len(stack)
		indent := strings.Repeat("  ", depth)
		switch {
		case e.Instant:
			_, _ = fmt.Fprintf(w, "%8.2fms %s· %s %s\n",
				float64(e.Start)/1e6, indent, e.Name, fmtAttrs(e.Attrs))
		default:
			_, _ = fmt.Fprintf(w, "%8.2fms %s%s  %s %s\n",
				float64(e.Start)/1e6, indent, dur(time.Duration(e.Dur)), e.Name, fmtAttrs(e.Attrs))
			stack = append(stack, spanEnd(e))
		}
	}
}

func fmtAttrs(attrs []Attr) string {
	if len(attrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("%s=%v", a.Key, a.Value))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// printSummary writes the short stderr view: the ten costliest spans and every
// non-zero counter. The full picture is in the files.
func printSummary(w io.Writer, events []event, total, dropped int64) {
	rows := aggregate(events)
	_, _ = fmt.Fprintf(w, "\n⏱  trace  (%s wall, %d spans)\n", dur(time.Duration(total)), len(events))
	for i, r := range rows {
		if i >= 10 {
			_, _ = fmt.Fprintf(w, "   … %d more spans in the report\n", len(rows)-i)
			break
		}
		pct := 0.0
		if total > 0 {
			pct = float64(r.total) / float64(total) * 100
		}
		_, _ = fmt.Fprintf(w, "   %-10s %-34s n=%-5d %9s  %5.1f%%\n",
			r.cat, truncate(r.name, 34), r.count, dur(r.total), pct)
	}
	for _, c := range counterRows() {
		_, _ = fmt.Fprintf(w, "   #  %-44s %12d\n", c.name, c.value)
	}
	if dropped > 0 {
		_, _ = fmt.Fprintf(w, "   !  %d events dropped (cap %d)\n", dropped, maxEvents)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// dur formats a duration keeping sub-millisecond resolution, which most of these
// spans need once the obvious costs are gone.
func dur(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1e3)
	case d < time.Second:
		return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1e6)
	default:
		return fmt.Sprintf("%.3fs", d.Seconds())
	}
}
